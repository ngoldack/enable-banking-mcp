#!/bin/sh
# Entrypoint for the OTel eBPF auto-instrumented runtime image.
#
# The OpenTelemetry Go auto-instrumentation agent is a *separate* privileged
# process that attaches to a running target binary via eBPF. It does not exec
# the target itself; it discovers it through OTEL_GO_AUTO_TARGET_EXE.
#
# This script therefore:
#   1. Starts the application in the background.
#   2. Starts the eBPF agent, which attaches to that application.
#   3. Forwards termination signals for a graceful shutdown.
#   4. Exits with the application's exit code once it terminates.
#
# Written for POSIX /bin/sh (Alpine busybox) — no bashisms (e.g. `wait -n`).
set -u

app_bin="/app/fin-mcp"
agent_bin="/otel-go-instrumentation"

# 1. Launch the target application with whatever args were passed (CMD / docker run).
"$app_bin" "$@" &
app_pid=$!

# 2. Launch the eBPF auto-instrumentation agent (attaches via OTEL_GO_AUTO_TARGET_EXE).
"$agent_bin" &
agent_pid=$!

# 3. Forward SIGTERM/SIGINT to both children for a clean shutdown.
terminate() {
	kill -TERM "$app_pid" "$agent_pid" 2>/dev/null || true
}
trap terminate TERM INT

# 4. Wait for the primary application process. A trapped signal interrupts `wait`,
#    so loop until the process is truly gone, then reap the agent.
status=0
while kill -0 "$app_pid" 2>/dev/null; do
	wait "$app_pid"
	status=$?
done

kill -TERM "$agent_pid" 2>/dev/null || true
wait "$agent_pid" 2>/dev/null || true

exit "$status"
