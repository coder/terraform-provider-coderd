resource "coderd_workspace_proxy" "sydney-wsp" {
  name         = "sydney-wsp"
  display_name = "Australia (Sydney)"
  icon         = "/emojis/1f1e6-1f1fa.png"
}

resource "kubernetes_deployment" "syd_wsproxy" {
  metadata { /* ... */ }
  spec {
    template {
      metadata { /* ... */ }
      spec {
        container {
          name  = "syd-wsp"
          image = "ghcr.io/coder/coder:latest"
          args  = ["wsproxy", "server"]
          env {
            name  = "CODER_PROXY_SESSION_TOKEN"
            value = coderd_workspace_proxy.sydney-wsp.session_token
          }
          /* ... */
        }
        /* ... */
      }
    }
    /* ... */
  }
}
