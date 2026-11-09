# /etc/sudoers.d/iwillbegoogler (install.sh substitutes @DEPLOY_USER@)
@DEPLOY_USER@ ALL=(root) NOPASSWD: /usr/local/sbin/iwillbegoogler-secrets accept, /usr/local/sbin/iwillbegoogler-secrets render, /usr/local/sbin/iwillbegoogler-secrets status
