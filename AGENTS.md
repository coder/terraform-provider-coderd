# terraform-provider-coderd

You are an experienced, pragmatic software engineering AI agent. Do not over-engineer a solution when a simple one is possible. Keep edits minimal. If you want an exception to ANY rule, you MUST stop and get permission first.

## Project Overview

Terraform provider for managing a [Coder](https://coder.com) deployment (`registry.terraform.io/coder/coderd`). It wraps the Coder API via the `github.com/coder/coder/v2/codersdk` client to manage templates, users, groups, organizations, licenses, workspace proxies, provisioner keys, org/group sync, and AI providers.

- **Language:** Go (toolchain pinned in `go.mod`).
- **Framework:** [terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework) (v1.x), **not** the legacy SDKv2. Use framework idioms (`schema.*Attribute`, `types.*`, validators, plan modifiers, `ResourceWithValidateConfig`).
- **Key deps:** `terraform-plugin-framework-validators`, `terraform-plugin-docs` (docs generation), `terraform-plugin-testing` (acceptance tests), and the Coder SDK (`codersdk`).
- **Source of truth:** the Coder server. Prefer the SDK's request/response structs and its `Validate()` methods over reimplementing API rules locally.

## Reference

- `internal/provider/*_resource.go` — one file per resource (model structs, schema, CRUD, validators). Tests live alongside in `*_resource_test.go`.
- `internal/provider/provider.go` — `CoderdProvider`, `Configure()`, the resource/data-source registry, and `CoderdProviderData` (shared client + cached feature entitlements).
- `internal/provider/util.go` — shared helpers: `isNotFound`, `stringValueOrNull`, `memberDiff`, `computeDirectoryHash`, `corsPtr`, `PrintOrNull`.
- `internal/provider/uuid.go` — custom `UUID` framework type (Terraform can't produce `[]uuid.UUID` from a set directly).
- `internal/provider/provider_test.go`, `provider_headers_test.go` — test harness: `testAccProtoV6ProviderFactories` and `newMockServer(...)`.
- `docs/` and `examples/` — **generated/curated**; `docs/` is produced by `make gen` and CI fails if it drifts. `examples/resources/<type>/` feeds the docs.
- `integration/` — container-based integration tests (separate from unit/acceptance).
- `main.go` — provider entrypoint and the `//go:generate tfplugindocs` directive.

Each resource implements the framework `Resource` interface (`Metadata`/`Schema`/`Configure`/`Create`/`Read`/`Update`/`Delete`), often `ResourceWithImportState`, and sometimes `ResourceWithValidateConfig` or `ResourceWithModifyPlan`.

## Essential commands

```bash
make build                 # CGO_ENABLED=0 go build .
make fmt                   # go fmt ./... && terraform fmt -recursive ./examples
make lint                  # golangci-lint run ./...
make gen                   # go generate ./... (regenerates docs/ from schema + examples/)
gofmt -l <files>           # check specific files are formatted

# Focused unit/validation tests (no Coder server needed):
TF_ACC= go test ./internal/provider -run '^TestName$' -count=1

# Full acceptance suite (needs TF_ACC=1 + a Coder server/license). Avoid running all of it casually:
make testacc               # TF_ACC=1 go test ./... -timeout 120m
```

`make gen` requires `terraform` on PATH. The `//go:generate` directive must pass `--provider-name coderd`; otherwise `tfplugindocs` infers the name from the working-directory/branch and writes wrong doc paths.

## Patterns

- **Secrets via write-only arguments (Terraform >= 1.11).** New secret-bearing attributes use `WriteOnly: true` + `Sensitive: true` paired with a normal `*_wo_version` trigger argument (bump the version to re-send). Read write-only values from `req.Config` only — never from `req.Plan` or state; the framework nullifies them in state regardless. Constraints: write-only attrs cannot be `Computed`; set attributes cannot be write-only or contain write-only descendants (use a map keyed by a local alias instead); a nested parent of a write-only child must not be `Computed`.
- **Prefer built-in validators over hand-rolled checks.** Use `stringvalidator.{OneOf,LengthAtLeast,RegexMatches,AlsoRequires}`, `resourcevalidator.{RequiredTogether,Conflicting,ExactlyOneOf,...}`, and `path.MatchRoot(...).AtName(...)` expressions. Reserve `ValidateConfig` for conditional/cross-field rules built-ins can't express (e.g. discriminator-dependent requirements).
- **Entitlements are cached and shared.** `Configure()` fetches `client.Entitlements()` once into `CoderdProviderData` (`Features()`/`SetFeatures()` are mutex-guarded). Gate premium features with a `Check<X>Entitlements(ctx, features)` helper that emits a clear diagnostic (mirror `CheckGroupEntitlements`). After a resource changes entitlements at apply time (license create/delete), it must re-fetch and `SetFeatures(...)` so later resources in the same apply see fresh flags (see Anti-patterns).
- **Drift / external deletion.** `isNotFound` treats both HTTP 404 **and** the 400 `"must be an existing uuid or username"` as not-found. Coder *tombstones* some objects (a deleted user still returns from GET-by-ID), so detect deletion with a secondary lookup (e.g. by username) and `resp.State.RemoveResource(ctx)` rather than trusting GET-by-ID.
- **"Unmanaged" via null.** A `null` block/attribute can mean "Terraform does not manage this facet" (e.g. `coderd_user.roles = null` skips role read/update so OIDC role-sync doesn't fight the provider). Don't synthesize remote values into state for unmanaged facets.
- **Use SDK pointer fields for optional updates** so an explicit `false`/zero is *sent* rather than omitted, and only send update requests when a value actually changed (avoid spurious PATCHes equal to the default or the server-computed value).

## Anti-patterns (each learned from a past fix — don't reintroduce)

- **Required ≠ known.** A `Required` attribute can still be **unknown** at validate/plan time when sourced from an input variable, module output, or computed reference. `ValidateConfig` and plan modifiers run during the validate walk where required vars are unknown. Always guard with `IsUnknown()` and **defer** (return without error) when a value you depend on is unknown — built-in validators already do this. (this session's AI-provider fix)
- **Don't decode unknown collections into native Go slices.** `ElementsAs` into `[]T` panics/errors on unknown sets/lists with "Received unknown value, however the target type cannot handle unknown values." Model such attributes as `types.Set`/`types.List` and only convert to `[]T` once `!IsUnknown() && !IsNull()`. (#305, #347, #362)
- **Don't rewrite whole nested collections in plan modifiers — it strips cty sensitivity marks.** `types.ListValueFrom(...)` reconstructs values and drops Terraform core's sensitivity marks, causing "Provider produced inconsistent final plan: inconsistent values for sensitive attribute". Write only the single field you need via `resp.Plan.SetAttribute(...)`. (#343)
- **Never `defer` inside a `for`/retry loop.** Go runs `defer` at function return, not loop-iteration end, so closers accumulate (and historically caused a nil-deref SIGSEGV). Extract the loop body into its own function (e.g. `waitForJobOnce`). (#308)
- **Don't assume entitlements from `Configure()` stay valid.** They're fetched once before any resource is created; a `coderd_license` applied in the same run leaves later resources seeing stale flags unless entitlements are refreshed. (#306)
- **Don't default a server-computed field to `""` and send it.** Some Coder fields are server-computed (e.g. organization `display_name` defaults to `name`); sending an empty default causes drift/spurious updates. Mirror server behavior or leave it `Computed`. (#183, #190)

## Testing patterns

- **Schema/`ValidateConfig` errors:** use `resource.Test` with `IsUnitTest: true` and `ExpectError: regexp.MustCompile(...)`. These fire before `Configure()`, so **no server is needed**.
- **Tests that reach plan/apply need a reachable server.** `Configure()` calls `client.User(ctx, Me)` (to resolve the default org) and `client.Entitlements(ctx)`, so a bogus URL fails with connection-refused even for `PlanOnly`. Use `newMockServer(nil)` (from `provider_headers_test.go`) for plan-only/deferral unit tests.
- **Deferral tests:** inject unknown values with a `terraform_data.x.output` reference, then assert the plan succeeds using `PlanOnly: true` + `ExpectNonEmptyPlan: true` (PlanOnly with a non-empty plan otherwise errors with "The non-refresh plan was not empty").
- **Reproduce the "unknown var" class of bug** with required (no-default) variables via `TestStep.ConfigVariables`: the validate walk evaluates required vars as unknown, which is exactly where the `#305` family of bugs surfaced. Literal-interpolated configs and vars-with-defaults do *not* catch it.
- **Acceptance tests** (`TestAcc*`) are gated by `TF_ACC=1`, share one Coder instance, and therefore **cannot run subtests in parallel** — hence golangci's `paralleltest.ignore-missing-subtests: true`. Use `statecheck`/`ConfigPlanChecks` to assert plan/state.

## Commit and Pull Request Guidelines

Before committing, run and ensure clean:

```bash
make fmt && git diff --exit-code     # CI fails on unformatted code
make gen && git diff --exit-code     # CI fails if generated docs drift
make lint
go build ./...
TF_ACC= go test ./internal/provider -run '<focused>' -count=1
```

- **Commit messages:** Conventional Commits — `type(scope): summary` (`fix:`, `feat:`, `chore:`, `test:`; scope like `coderd_user` or `internal/provider`). Squash-merge appends the PR number, e.g. `fix: handle unknown tf_vars at plan time (#362)`.
- **PR descriptions:** explain the problem (with the failing error/repro) and the fix; reference issues (`Closes #208`, `Refs #305`). Do not hard-wrap body lines. Do **not** add a "Testing" section that just lists tests you ran — CI covers acceptance testing across the Terraform version matrix.
- **Docs:** when a schema or example changes, regenerate with `make gen` and commit the updated `docs/` and `examples/` together with the code.
- **Terraform version note:** the CI acceptance matrix runs TF 1.0–1.9, but write-only (`*_wo`) arguments require TF >= 1.11 when configured; document that requirement on any resource that uses them.
