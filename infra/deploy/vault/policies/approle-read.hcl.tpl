# AppRole iwillbegoogler-@ENV@: read-only on its KV path
path "kv/data/iwillbegoogler/@ENV@" {
  capabilities = ["read"]
}
