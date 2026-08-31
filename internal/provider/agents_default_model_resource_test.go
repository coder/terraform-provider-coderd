package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

func TestAgentsDefaultModelStateFromModelConfig(t *testing.T) {
	t.Parallel()

	organizationID := uuid.New()
	modelID := uuid.New()
	state := stateFromAgentsDefaultModelConfig(codersdk.ChatModel{
		ID:             modelID,
		OrganizationID: organizationID,
		IsDefault:      true,
	})
	require.Equal(t, organizationID, state.ID.ValueUUID())
	require.Equal(t, organizationID.String(), state.ID.ValueString())
	require.Equal(t, organizationID, state.OrganizationID.ValueUUID())
	require.Equal(t, organizationID.String(), state.OrganizationID.ValueString())
	require.Equal(t, modelID, state.ModelID.ValueUUID())
	require.Equal(t, modelID.String(), state.ModelID.ValueString())
}

func TestAgentsDefaultModelMoveState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	modelID := uuid.New()
	r := &AgentsDefaultModelResource{}
	movers := r.MoveState(ctx)
	require.Len(t, movers, 1)
	require.NotNil(t, movers[0].SourceSchema)

	sourceSchema := *movers[0].SourceSchema
	sourceState := tfsdk.State{
		Schema: sourceSchema,
		Raw:    tftypes.NewValue(sourceSchema.Type().TerraformType(ctx), nil),
	}
	require.False(t, sourceState.Set(ctx, legacyDefaultAgentsModelResourceModel{
		ID:      types.StringValue("default"),
		ModelID: types.StringValue(modelID.String()),
	}).HasError())

	var targetSchemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &targetSchemaResp)
	require.False(t, targetSchemaResp.Diagnostics.HasError(), targetSchemaResp.Diagnostics)
	targetSchema := targetSchemaResp.Schema
	resp := &fwresource.MoveStateResponse{
		TargetState: tfsdk.State{
			Schema: targetSchema,
			Raw:    tftypes.NewValue(targetSchema.Type().TerraformType(ctx), nil),
		},
	}
	movers[0].StateMover(ctx, fwresource.MoveStateRequest{
		SourceProviderAddress: "registry.example.com/coder/coderd",
		SourceSchemaVersion:   0,
		SourceState:           &sourceState,
		SourceTypeName:        "coderd_default_agents_model",
	}, resp)
	require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

	var got AgentsDefaultModelResourceModel
	require.False(t, resp.TargetState.Get(ctx, &got).HasError())
	require.True(t, got.ID.IsNull())
	require.True(t, got.OrganizationID.IsNull())
	require.Equal(t, modelID, got.ModelID.ValueUUID())

	for _, tc := range []struct {
		name string
		req  fwresource.MoveStateRequest
	}{
		{
			name: "wrong source type",
			req: fwresource.MoveStateRequest{
				SourceProviderAddress: "registry.example.com/coder/coderd",
				SourceSchemaVersion:   0,
				SourceState:           &sourceState,
				SourceTypeName:        "coderd_other",
			},
		},
		{
			name: "wrong schema version",
			req: fwresource.MoveStateRequest{
				SourceProviderAddress: "registry.example.com/coder/coderd",
				SourceSchemaVersion:   1,
				SourceState:           &sourceState,
				SourceTypeName:        "coderd_default_agents_model",
			},
		},
		{
			name: "wrong provider address",
			req: fwresource.MoveStateRequest{
				SourceProviderAddress: "registry.example.com/hashicorp/coderd",
				SourceSchemaVersion:   0,
				SourceState:           &sourceState,
				SourceTypeName:        "coderd_default_agents_model",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := &fwresource.MoveStateResponse{
				TargetState: tfsdk.State{
					Schema: targetSchema,
					Raw:    tftypes.NewValue(targetSchema.Type().TerraformType(ctx), nil),
				},
			}
			movers[0].StateMover(ctx, tc.req, resp)
			require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)
			require.True(t, resp.TargetState.Raw.IsNull())
		})
	}
}

func TestAgentsDefaultModelIDPlanModifier(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	oldOrganizationID := uuid.New()
	newOrganizationID := uuid.New()
	modelID := uuid.New()

	r := &AgentsDefaultModelResource{}
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError(), schemaResp.Diagnostics)

	idAttribute, ok := schemaResp.Schema.Attributes["id"].(schema.StringAttribute)
	require.True(t, ok)
	require.Len(t, idAttribute.PlanModifiers, 1)
	modifier := idAttribute.PlanModifiers[0]

	raw := func(id tftypes.Value, organizationID uuid.UUID) tftypes.Value {
		return tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
			"id":              id,
			"organization_id": tftypes.NewValue(tftypes.String, organizationID.String()),
			"model_id":        tftypes.NewValue(tftypes.String, modelID.String()),
		})
	}
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    raw(tftypes.NewValue(tftypes.String, oldOrganizationID.String()), oldOrganizationID),
	}

	for _, tc := range []struct {
		name                string
		plannedOrganization uuid.UUID
		want                types.String
	}{
		{
			name:                "retains id when organization is unchanged",
			plannedOrganization: oldOrganizationID,
			want:                types.StringValue(oldOrganizationID.String()),
		},
		{
			name:                "leaves id unknown when organization changes",
			plannedOrganization: newOrganizationID,
			want:                types.StringUnknown(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := tfsdk.Plan{
				Schema: schemaResp.Schema,
				Raw:    raw(tftypes.NewValue(tftypes.String, tftypes.UnknownValue), tc.plannedOrganization),
			}
			resp := &planmodifier.StringResponse{PlanValue: types.StringUnknown()}
			modifier.PlanModifyString(ctx, planmodifier.StringRequest{
				ConfigValue: types.StringNull(),
				PlanValue:   types.StringUnknown(),
				StateValue:  types.StringValue(oldOrganizationID.String()),
				Plan:        plan,
				State:       state,
			}, resp)

			require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)
			require.Equal(t, tc.want, resp.PlanValue)
		})
	}
}

func TestAgentsDefaultModelPatch404Diagnostics(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                   string
		targetCollectionStatus int
		organizationStatus     int
		wantSummary            string
		wantDetailContains     []string
		wantRequestCount       int32
	}{
		{
			name:                   "model missing",
			targetCollectionStatus: http.StatusOK,
			wantSummary:            "Default Agents Model Not Found or Inaccessible",
			wantRequestCount:       2,
		},
		{
			name:                   "endpoint unavailable",
			targetCollectionStatus: http.StatusNotFound,
			organizationStatus:     http.StatusOK,
			wantSummary:            "Agents Default Model Endpoint Unavailable",
			wantDetailContains:     []string{agentsDefaultModelMinVersion},
			wantRequestCount:       3,
		},
		{
			name:                   "organization missing",
			targetCollectionStatus: http.StatusNotFound,
			organizationStatus:     http.StatusNotFound,
			wantSummary:            "Organization Not Found or Inaccessible",
			wantRequestCount:       3,
		},
		{
			name:                   "organization probe fails",
			targetCollectionStatus: http.StatusNotFound,
			organizationStatus:     http.StatusInternalServerError,
			wantSummary:            "Client Error",
			wantDetailContains:     []string{"Organization probe error:"},
			wantRequestCount:       3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			targetOrganizationID := uuid.New()
			defaultOrganizationID := uuid.New()
			modelID := uuid.New()
			targetCollectionPath := fmt.Sprintf("/api/v2/organizations/%s/chats/models", targetOrganizationID)
			modelPath := fmt.Sprintf("%s/%s", targetCollectionPath, modelID)
			organizationPath := fmt.Sprintf("/api/v2/organizations/%s", targetOrganizationID)
			var requestCount atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requestCount.Add(1)
				switch {
				case req.Method == http.MethodPatch && req.URL.Path == modelPath:
					writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
				case req.Method == http.MethodGet && req.URL.Path == targetCollectionPath:
					writeAgentsDefaultModelCollectionResponse(w, tc.targetCollectionStatus)
				case req.Method == http.MethodGet && req.URL.Path == organizationPath:
					if tc.organizationStatus == http.StatusOK {
						writeJSON(w, http.StatusOK, codersdk.Organization{MinimalOrganization: codersdk.MinimalOrganization{ID: targetOrganizationID, Name: "target"}})
						return
					}
					writeJSON(w, tc.organizationStatus, codersdk.Response{Message: statusMessage(tc.organizationStatus)})
				default:
					writeJSON(w, http.StatusInternalServerError, codersdk.Response{Message: "unexpected request"})
				}
			}))
			t.Cleanup(srv.Close)

			r := newAgentsDefaultModelTestResource(t, srv.URL, defaultOrganizationID)
			_, err := r.setDefault(t.Context(), targetOrganizationID, modelID)
			require.Error(t, err)

			diags := r.agentsDefaultModelDiag(t.Context(), "set", targetOrganizationID, modelID, err)
			require.Len(t, diags.Errors(), 1)
			require.Equal(t, tc.wantSummary, diags.Errors()[0].Summary())
			require.Contains(t, diags.Errors()[0].Detail(), "Original error:")
			require.Contains(t, diags.Errors()[0].Detail(), "Not Found.")
			for _, want := range tc.wantDetailContains {
				require.Contains(t, diags.Errors()[0].Detail(), want)
			}
			require.Equal(t, tc.wantRequestCount, requestCount.Load())
		})
	}
}

func TestAgentsDefaultModelReadCollection404(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                           string
		targetCollectionStatus         int
		providerDefaultMatchesTargetID bool
	}{
		{
			name:                           "configured provider default organization missing",
			targetCollectionStatus:         http.StatusNotFound,
			providerDefaultMatchesTargetID: true,
		},
		{
			name:                   "other organization missing",
			targetCollectionStatus: http.StatusNotFound,
		},
		{
			name:                   "organization missing with Coder 400 response",
			targetCollectionStatus: http.StatusBadRequest,
		},
		{
			name:                   "empty supported collection",
			targetCollectionStatus: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			targetOrganizationID := uuid.New()
			defaultOrganizationID := uuid.New()
			if tc.providerDefaultMatchesTargetID {
				defaultOrganizationID = targetOrganizationID
			}
			modelID := uuid.New()
			targetCollectionPath := fmt.Sprintf("/api/v2/organizations/%s/chats/models", targetOrganizationID)
			var requestCount atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requestCount.Add(1)
				if req.URL.Path != targetCollectionPath {
					writeJSON(w, http.StatusInternalServerError, codersdk.Response{Message: "unexpected request"})
					return
				}
				writeAgentsDefaultModelCollectionResponse(w, tc.targetCollectionStatus)
			}))
			t.Cleanup(srv.Close)

			r := newAgentsDefaultModelTestResource(t, srv.URL, defaultOrganizationID)
			state := agentsDefaultModelTestState(t, r, AgentsDefaultModelResourceModel{
				ID:             UUIDValue(targetOrganizationID),
				OrganizationID: UUIDValue(targetOrganizationID),
				ModelID:        UUIDValue(modelID),
			})
			resp := &fwresource.ReadResponse{State: state}
			r.Read(t.Context(), fwresource.ReadRequest{State: state}, resp)

			require.True(t, resp.State.Raw.IsNull())
			require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)
			require.Equal(t, int32(1), requestCount.Load(), "expected only the resource organization's collection to be requested")
		})
	}
}

func newAgentsDefaultModelTestResource(t *testing.T, serverURL string, defaultOrganizationID uuid.UUID) *AgentsDefaultModelResource {
	t.Helper()

	parsedURL, err := url.Parse(serverURL)
	require.NoError(t, err)
	return &AgentsDefaultModelResource{data: &CoderdProviderData{
		Client:                codersdk.New(parsedURL),
		DefaultOrganizationID: defaultOrganizationID,
	}}
}

func agentsDefaultModelTestState(t *testing.T, r *AgentsDefaultModelResource, model AgentsDefaultModelResourceModel) tfsdk.State {
	t.Helper()

	ctx := t.Context()
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError(), schemaResp.Diagnostics)

	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	require.False(t, state.Set(ctx, &model).HasError())
	return state
}

func writeAgentsDefaultModelCollectionResponse(w http.ResponseWriter, status int) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status == http.StatusBadRequest {
		writeJSON(w, status, codersdk.Response{Message: "must be an existing uuid or username"})
		return
	}
	if status != http.StatusOK {
		writeJSON(w, status, codersdk.Response{Message: statusMessage(status)})
		return
	}
	writeJSON(w, http.StatusOK, codersdk.OrganizationChatModelsResponse{})
}

// TestAgentsDefaultModelResourceValidationDefersUnknownConfig checks validation
// passes when model_id is unknown, like when it comes from an unset variable.
func TestAgentsDefaultModelResourceValidationDefersUnknownConfig(t *testing.T) {
	t.Parallel()

	// PlanOnly reaches provider Configure(), which fetches the current user
	// and entitlements, so use a mock server instead of an unreachable URL.
	srv := newMockServer(nil)
	defer srv.Close()

	cfg := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}

variable "model_id" {
  type = string
}

resource "coderd_agents_default_model" "default" {
  organization_id = "` + uuid.NewString() + `"
  model_id        = var.model_id
}
`
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// model_id is unknown during the validate walk even though
				// ConfigVariables supplies a concrete plan value.
				Config: cfg,
				ConfigVariables: config.Variables{
					"model_id": config.StringVariable(uuid.NewString()),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAgentsDefaultModelResourceDefersUnknownOrganizationID checks planning
// succeeds when organization_id comes from another resource and is therefore
// unknown until apply.
func TestAgentsDefaultModelResourceDefersUnknownOrganizationID(t *testing.T) {
	t.Parallel()

	srv := newMockServer(nil)
	defer srv.Close()

	cfg := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}

variable "organization_id" {
  type = string
}

resource "terraform_data" "organization" {
  input = var.organization_id
}

resource "coderd_agents_default_model" "default" {
  organization_id = terraform_data.organization.output
  model_id        = "` + uuid.NewString() + `"
}
`
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigVariables: config.Variables{
					"organization_id": config.StringVariable(uuid.NewString()),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAgentsDefaultModelMovedBlockMigration reproduces the coder/dogfood
// v0.0.23 -> v0.0.24 upgrade (coder/dogfood#453): step 1 writes real
// coderd_default_agents_model state with the legacy schema, step 2 runs the
// real provider with a `moved` block to coderd_agents_default_model, an
// adopted coderd_agents_model, and organization_id sourced from
// data.coderd_organization (is_default = true). Terraform core invokes the
// MoveResourceState RPC before ConfigureProvider (hashicorp/terraform#35922),
// so the mover must work without provider data; before the fix this plan
// failed with "The provider was not configured before Terraform attempted to
// move coderd_default_agents_model state.".
func TestAgentsDefaultModelMovedBlockMigration(t *testing.T) {
	// The state mover only accepts source addresses from coder/coderd, and the
	// test framework defaults to the hashicorp namespace. t.Setenv also
	// prevents t.Parallel().
	t.Setenv(resource.EnvTfAccProviderNamespace, "coder")

	orgID := uuid.New()
	ts := time.Unix(1700000000, 0).UTC()
	model := codersdk.ChatModel{
		ID:                   uuid.New(),
		OrganizationID:       orgID,
		AIProviderID:         uuid.New(),
		Model:                "claude-3-5-sonnet-20241022",
		DisplayName:          "Claude Sonnet",
		Enabled:              true,
		ContextLimit:         200000,
		CompressionThreshold: 70,
		CreatedAt:            ts,
		UpdatedAt:            ts,
	}

	var modelPatched, defaultPatched atomic.Bool
	srv := fakeAgentsDefaultModelMigrationServer(t, orgID, model, &modelPatched, &defaultPatched)

	providerBlock := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}
`
	modelArgs := `  ai_provider_id = "` + model.AIProviderID.String() + `"
  model          = "` + model.Model + `"
  context_limit  = 200000
}
`
	legacyFactories := map[string]func() (tfprotov6.ProviderServer, error){
		"coderd": providerserver.NewProtocol6WithError(&legacyCoderdProvider{model: model}),
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest: true,
		Steps: []resource.TestStep{
			{
				ProtoV6ProviderFactories: legacyFactories,
				Config: providerBlock + `
resource "coderd_agents_model" "test" {
` + modelArgs + `
resource "coderd_default_agents_model" "this" {
  model_id = coderd_agents_model.test.id
}
`,
			},
			{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Config: providerBlock + `
data "coderd_organization" "default" {
  is_default = true
}

resource "coderd_agents_model" "test" {
  organization_id = data.coderd_organization.default.id
` + modelArgs + `
moved {
  from = coderd_default_agents_model.this
  to   = coderd_agents_default_model.this
}

resource "coderd_agents_default_model" "this" {
  organization_id = data.coderd_organization.default.id
  model_id        = coderd_agents_model.test.id
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_agents_model.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("coderd_agents_default_model.this", plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("coderd_agents_default_model.this", tfjsonpath.New("id"), knownvalue.StringExact(orgID.String())),
					statecheck.ExpectKnownValue("coderd_agents_default_model.this", tfjsonpath.New("organization_id"), knownvalue.StringExact(orgID.String())),
					statecheck.ExpectKnownValue("coderd_agents_default_model.this", tfjsonpath.New("model_id"), knownvalue.StringExact(model.ID.String())),
					statecheck.ExpectKnownValue("coderd_agents_model.test", tfjsonpath.New("organization_id"), knownvalue.StringExact(orgID.String())),
				},
			},
		},
	})

	require.True(t, modelPatched.Load(), "expected the model adoption apply to PATCH the organization-scoped route")
	require.True(t, defaultPatched.Load(), "expected the moved default model apply to PATCH is_default on the organization-scoped route")
}

// fakeAgentsDefaultModelMigrationServer serves everything the moved-block
// migration exercises: provider Configure, the coderd_organization data
// source, and the organization-scoped chat model routes. PATCHes carrying
// is_default are recorded separately from model updates because both
// resources share the same route.
func fakeAgentsDefaultModelMigrationServer(t *testing.T, orgID uuid.UUID, model codersdk.ChatModel, modelPatched, defaultPatched *atomic.Bool) *httptest.Server {
	t.Helper()

	defaultModel := model
	defaultModel.IsDefault = true
	modelPath := fmt.Sprintf("/api/v2/organizations/%s/chats/models/%s", orgID, model.ID)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// Configure fetches the current user and entitlements; the user
		// payload decodes acceptably for both.
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"%s","username":"admin","organization_ids":["%s"]}`, uuid.NewString(), orgID)
	})
	mux.HandleFunc("GET /api/v2/organizations/default", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, codersdk.Organization{
			MinimalOrganization: codersdk.MinimalOrganization{ID: orgID, Name: "default"},
			IsDefault:           true,
		})
	})
	mux.HandleFunc(fmt.Sprintf("GET /api/v2/organizations/%s/members/", orgID), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, []codersdk.OrganizationMemberWithUserData{})
	})
	mux.HandleFunc("GET /api/v2/ai/providers/"+model.AIProviderID.String(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, codersdk.AIProvider{ID: model.AIProviderID, Type: "anthropic"})
	})
	mux.HandleFunc("GET "+modelPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, model)
	})
	mux.HandleFunc("PATCH "+modelPath, func(w http.ResponseWriter, r *http.Request) {
		var req codersdk.UpdateChatModelRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		if req.IsDefault != nil && *req.IsDefault {
			defaultPatched.Store(true)
			writeJSON(w, http.StatusOK, defaultModel)
			return
		}
		modelPatched.Store(true)
		writeJSON(w, http.StatusOK, model)
	})
	mux.HandleFunc("DELETE "+modelPath, func(w http.ResponseWriter, _ *http.Request) {
		// Post-test destroy cleanup.
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(fmt.Sprintf("GET /api/v2/organizations/%s/chats/models", orgID), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, codersdk.OrganizationChatModelsResponse{Models: []codersdk.ChatModel{defaultModel}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAccAgentsDefaultModelResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_default_model_acc", integration.UseLicense)
	organization := accDefaultOrganization(ctx, t, client)
	organizationID := organization.ID
	skipIfAgentsDefaultModelUnsupported(ctx, t, client, organizationID)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	cfg := func(defaultModel string) string {
		return fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = %q
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = 200000
}

resource "coderd_agents_model" "opus" {
  ai_provider_id = %q
  model          = "claude-3-opus-20240229"
  context_limit  = 200000
}

resource "coderd_agents_default_model" "default" {
  organization_id = %q
  model_id        = coderd_agents_model.%s.id
}
`, client.URL.String(), client.SessionToken(), aiProvider.ID.String(), aiProvider.ID.String(), organizationID.String(), defaultModel)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("sonnet"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "id", organizationID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "organization_id", organizationID.String()),
					resource.TestCheckResourceAttrPair("coderd_agents_default_model.default", "model_id", "coderd_agents_model.sonnet", "id"),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
			{
				// Re-point the default to opus. Coder demotes sonnet atomically in
				// the same operation, so exactly one model remains default.
				Config: cfg("opus"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("coderd_agents_default_model.default", "model_id", "coderd_agents_model.opus", "id"),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
			{
				// A steady-state re-plan must be empty (no perpetual diff).
				Config:   cfg("opus"),
				PlanOnly: true,
			},
			{
				// Import by organization name; Read resolves its current default.
				ResourceName:      "coderd_agents_default_model.default",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     organization.Name,
			},
		},
	})
}

// TestAccAgentsDefaultModelResourceDriftAndDelete proves two things against
// models created out-of-band (so they outlive the Terraform resource):
//
//   - Read detects an external change to the default and Terraform reconciles
//     back to the configured model.
//   - Delete is a no-op: Coder keeps exactly one model marked default, so
//     destroying the pointer leaves the server's default untouched.
func TestAccAgentsDefaultModelResourceDriftAndDelete(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_default_model_drift_acc", integration.UseLicense)
	organizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfAgentsDefaultModelUnsupported(ctx, t, client, organizationID)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	sonnet := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	opus := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-opus-20240229")
	exp := codersdk.NewExperimentalClient(client)

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_default_model" "default" {
  organization_id = %q
  model_id        = %q
}
`, client.URL.String(), client.SessionToken(), organizationID.String(), sonnet.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// Destroying the pointer must not clear the server default: Coder still
			// reports exactly one default, and it remains the last model we selected.
			defaults := serverDefaultModelIDs(ctx, t, client, organizationID)
			if len(defaults) != 1 {
				return fmt.Errorf("expected exactly one default model after destroy, got %d: %v", len(defaults), defaults)
			}
			if defaults[0] != sonnet.ID {
				return fmt.Errorf("expected default to remain %s after destroy, got %s", sonnet.ID, defaults[0])
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "model_id", sonnet.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
			{
				// Externally re-point the default to opus, then expect Terraform to
				// detect the drift on refresh and plan to restore sonnet.
				PreConfig: func() {
					_, err := exp.UpdateChatModel(ctx, organizationID, opus.ID, codersdk.UpdateChatModelRequest{
						IsDefault: new(true),
					})
					require.NoError(t, err, "externally set opus as default")
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// Re-applying reconciles the default back to sonnet.
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "model_id", sonnet.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
		},
	})
}

func TestAccAgentsDefaultModelResourceOrganizationIsolation(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_default_model_org_isolation_acc", integration.UseLicense)
	defaultOrganizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfAgentsDefaultModelUnsupported(ctx, t, client, defaultOrganizationID)

	otherOrganization, err := client.CreateOrganization(ctx, codersdk.CreateOrganizationRequest{
		Name:        "default-model-isolation",
		DisplayName: "Default Model Isolation",
	})
	require.NoError(t, err, "create second organization")
	t.Cleanup(func() {
		_ = client.DeleteOrganization(context.WithoutCancel(t.Context()), otherOrganization.ID.String())
	})

	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)
	createAccChatModel(ctx, t, client, defaultOrganizationID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	defaultOrgSecond := createAccChatModel(ctx, t, client, defaultOrganizationID, aiProvider.ID, "claude-3-opus-20240229")
	otherOrgFirst := createAccChatModel(ctx, t, client, otherOrganization.ID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	createAccChatModel(ctx, t, client, otherOrganization.ID, aiProvider.ID, "claude-3-opus-20240229")

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_default_model" "default_org" {
  organization_id = %q
  model_id        = %q
}

resource "coderd_agents_default_model" "other_org" {
  organization_id = %q
  model_id        = %q
}
`, client.URL.String(), client.SessionToken(), defaultOrganizationID.String(), defaultOrgSecond.ID.String(), otherOrganization.ID.String(), otherOrgFirst.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default_org", "id", defaultOrganizationID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.default_org", "organization_id", defaultOrganizationID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.default_org", "model_id", defaultOrgSecond.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.other_org", "id", otherOrganization.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.other_org", "organization_id", otherOrganization.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.other_org", "model_id", otherOrgFirst.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, defaultOrganizationID, "coderd_agents_default_model.default_org"),
					checkServerDefaultMatchesResource(ctx, t, client, otherOrganization.ID, "coderd_agents_default_model.other_org"),
				),
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}

func skipIfAgentsDefaultModelUnsupported(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID) {
	t.Helper()

	// Main devel builds report the previous minor's version, so a semver minimum
	// cannot distinguish them from releases that do not have this route.
	_, err := codersdk.NewExperimentalClient(client).ChatModels(ctx, organizationID)
	if err == nil {
		return
	}
	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		t.Skipf("deployment does not support org-scoped chat models")
	}
	require.NoError(t, err, "probe org-scoped chat models")
}

// createAccChatModel creates a chat model config directly via the SDK so it
// exists independently of any Terraform-managed resource.
func createAccChatModel(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID, aiProviderID uuid.UUID, model string) codersdk.ChatModel {
	t.Helper()
	exp := codersdk.NewExperimentalClient(client)
	created, err := exp.CreateChatModel(ctx, organizationID, codersdk.CreateChatModelRequest{
		AIProviderID: &aiProviderID,
		Model:        model,
		ContextLimit: new(int64(200000)),
	})
	require.NoError(t, err, "create chat model config out-of-band")
	// WithoutCancel: t.Context() is already cancelled by the time cleanup runs.
	t.Cleanup(func() { _ = exp.DeleteChatModel(context.WithoutCancel(t.Context()), organizationID, created.ID) })
	return created
}

// serverDefaultModelIDs returns the IDs of every model Coder reports as default
// in one organization. Coder enforces a single default per organization, so a
// healthy organization with models returns one ID.
func serverDefaultModelIDs(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID) []uuid.UUID {
	t.Helper()
	exp := codersdk.NewExperimentalClient(client)
	configs, err := exp.ChatModels(ctx, organizationID)
	require.NoError(t, err, "list chat models")
	var defaults []uuid.UUID
	for _, c := range configs.Models {
		if c.IsDefault {
			defaults = append(defaults, c.ID)
		}
	}
	return defaults
}

// checkServerDefaultMatchesResource asserts Coder reports exactly one default
// model in the organization and that it matches the named resource's model_id.
func checkServerDefaultMatchesResource(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID, resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		defaults := serverDefaultModelIDs(ctx, t, client, organizationID)
		if len(defaults) != 1 {
			return fmt.Errorf("expected exactly one default model in organization %s, got %d: %v", organizationID, len(defaults), defaults)
		}
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", resourceName)
		}
		if got := rs.Primary.Attributes["model_id"]; got != defaults[0].String() {
			return fmt.Errorf("server default %s does not match resource model_id %s", defaults[0], got)
		}
		return nil
	}
}
