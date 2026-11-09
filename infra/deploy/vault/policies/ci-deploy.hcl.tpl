# GitHub OIDC role iwillbegoogler-@ENV@-ci: manage secret_ids of AppRole iwillbegoogler-@ENV@ only
path "auth/approle/role/iwillbegoogler-@ENV@/secret-id" {
  capabilities = ["update", "list"]
}
path "auth/approle/role/iwillbegoogler-@ENV@/secret-id-accessor/destroy" {
  capabilities = ["update"]
}
path "auth/approle/role/iwillbegoogler-@ENV@/secret-id/destroy" {
  capabilities = ["update"]
}
