#!/bin/bash
# GitHub Actions self-hosted runner for the infra CD, as the deploy user, under systemd. Run as root:
#   RUNNER_TOKEN=<registration token> deploy/server/runner.sh
# Token: org IwillBeGoogler -> Settings -> Actions -> Runners -> New runner (valid 1 h); REPO_URL=https://github.com/IwillBeGoogler.
# Then org -> Settings -> Actions -> Runner groups: limit the group holding this runner to the Infra repository.
# Register it on the infra repo only (never on the app repos or an org): anyone who can push a workflow there can run
# code on this server as the deploy user. Protect main and the production environment (required reviewers).
set -euo pipefail
: "${RUNNER_TOKEN:?registration token}"
REPO_URL=${REPO_URL:-https://github.com/IwillBeGoogler}
RUNNER_VERSION=${RUNNER_VERSION:-2.338.2}
RUNNER_SHA256=${RUNNER_SHA256:-baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40} # from the GitHub "New runner" page
RUNNER_USER=${RUNNER_USER:-deploy}
DIR=/opt/actions-runner
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
id "$RUNNER_USER" >/dev/null

install -d -o "$RUNNER_USER" -m 0750 "$DIR"
if [ ! -x "$DIR/config.sh" ]; then
	tgz=$(mktemp)
	curl -fsSL -o "$tgz" "https://github.com/actions/runner/releases/download/v$RUNNER_VERSION/actions-runner-linux-x64-$RUNNER_VERSION.tar.gz"
	echo "$RUNNER_SHA256  $tgz" | sha256sum -c - >/dev/null || { echo "runner tarball hash mismatch" >&2; rm -f "$tgz"; exit 1; }
	tar xzf "$tgz" -C "$DIR"
	rm -f "$tgz"
	chown -R "$RUNNER_USER" "$DIR"
	"$DIR/bin/installdependencies.sh" >/dev/null
fi
if [ ! -f "$DIR/.runner" ]; then
	runuser -u "$RUNNER_USER" -- "$DIR/config.sh" --unattended --url "$REPO_URL" --token "$RUNNER_TOKEN" \
		--name "$(hostname)-prod" --labels iwillbegoogler-prod --no-default-labels --work _work --replace
fi
cd "$DIR"
./svc.sh install "$RUNNER_USER" >/dev/null 2>&1 || true
./svc.sh start
./svc.sh status | head -5
