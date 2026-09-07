#!/bin/sh
# install.sh sets up the Cyber Supplies CLI (cysu) for an agent in two steps:
#
#   1. npm install -g <package>     the compiled binary; no Go toolchain needed
#   2. npx skills add <source> -g   SKILL.md into every coding agent on the machine
#
# Usage:
#
#   curl -fsSL https://raw.githubusercontent.com/cyber-supplies/cysu-cli/main/install.sh | sh
#   sh install.sh
#
# Node.js (npm and npx) is the only requirement. When run from a terminal the
# skill installer asks which agents to install into; when piped through sh it
# installs into every agent it detects.
#
# Environment overrides:
#
#   CYSU_NPM_PACKAGE   npm package holding the binary   (default: @cyber-supplies/cysu)
#   CYSU_VERSION       npm version or dist-tag           (default: latest)
#   CYSU_SKILL_SOURCE  where `skills` reads SKILL.md     (default: cyber-supplies/cysu-cli)
#   CYSU_SKILL_AGENTS  space-separated agent names, e.g. "claude-code codex"
#                      (default: ask, or every detected agent when not a terminal)

set -eu

CYSU_NPM_PACKAGE="${CYSU_NPM_PACKAGE:-@cyber-supplies/cysu}"
CYSU_VERSION="${CYSU_VERSION:-latest}"
CYSU_SKILL_SOURCE="${CYSU_SKILL_SOURCE:-cyber-supplies/cysu-cli}"
CYSU_SKILL_AGENTS="${CYSU_SKILL_AGENTS:-}"

# info prints a progress line.
info() {
	printf '==> %s\n' "$*" >&2
}

# fail prints an error and stops the install.
fail() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

# need_cmd stops the install when a required program is missing.
need_cmd() {
	command -v "$1" >/dev/null 2>&1 ||
		fail "$1 is required but was not found; install Node.js from https://nodejs.org and re-run"
}

# install_binary installs the CLI globally through npm.
install_binary() {
	info "Installing $CYSU_NPM_PACKAGE@$CYSU_VERSION"
	npm install -g "$CYSU_NPM_PACKAGE@$CYSU_VERSION" ||
		fail "npm install failed; for EACCES errors see https://docs.npmjs.com/resolving-eacces-permissions-errors-when-installing-packages-globally"
}

# check_path confirms the agent will be able to run `cysu`, and explains how
# to fix PATH when npm's global bin directory is not on it.
check_path() {
	if command -v cysu >/dev/null 2>&1; then
		info "cysu installed at $(command -v cysu)"
		return
	fi
	prefix="$(npm prefix -g 2>/dev/null || true)"
	printf '%s\n' \
		"install.sh: cysu is installed but not on PATH." \
		"  Add npm's global bin directory to PATH and open a new shell:" \
		"    ${prefix:-<npm prefix -g>}/bin   (macOS, Linux)" \
		"    ${prefix:-<npm prefix -g>}       (Windows)" >&2
}

# install_skill installs SKILL.md into the user's agents with the `skills` CLI.
# Agents are taken from CYSU_SKILL_AGENTS when set. Otherwise a terminal gets
# the interactive picker, and a non-interactive run installs into every
# detected agent.
install_skill() {
	info "Installing the cysu skill from $CYSU_SKILL_SOURCE"
	set -- add "$CYSU_SKILL_SOURCE" -g
	if [ -n "$CYSU_SKILL_AGENTS" ]; then
		for agent in $CYSU_SKILL_AGENTS; do
			set -- "$@" -a "$agent"
		done
		set -- "$@" -y
	elif [ ! -t 0 ]; then
		set -- "$@" -y
	fi
	npx -y skills "$@" || fail "skill installation failed"
}

# next_steps tells the user what the CLI still needs before an agent can use it.
next_steps() {
	printf '%s\n' \
		"" \
		"Done. Before an agent can use cysu:" \
		"  1. export CYSU_TOKEN=<your API token>" \
		"  2. cysu key init        (creates the encryption key; asks for a passphrase)" \
		"  3. cysu projects list   (checks the token works)" >&2
}

main() {
	need_cmd npm
	need_cmd npx
	install_binary
	check_path
	install_skill
	next_steps
}

main "$@"
