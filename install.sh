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
# Node.js (npm and npx) and git are required. The skill installer asks which
# agents to install into, reading the answer from the terminal directly so
# the question is asked even when the script is piped through sh. Only a run
# with no terminal at all (CI) installs into every agent it detects.
#
# The script clones the repository itself and hands the installer the local
# checkout. Letting the installer clone while it holds the terminal makes it
# hang at "Cloning repository", so the two are kept apart.
#
# Environment overrides:
#
#   CYSU_NPM_PACKAGE   npm package holding the binary   (default: @cyber-supplies/cysu)
#   CYSU_VERSION       npm version or dist-tag           (default: latest)
#   CYSU_SKILL_SOURCE  GitHub owner/repo, git URL or local directory holding
#                      the skill                        (default: cyber-supplies/cysu-cli)
#   CYSU_SKILL_AGENTS  space-separated agent names, e.g. "claude-code codex"
#                      (default: ask; every detected agent when there is no terminal)

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

# skill_checkout resolves CYSU_SKILL_SOURCE to a local directory in
# CYSU_SKILL_DIR. A directory is used as it is; a git URL or GitHub
# owner/repo is cloned shallowly into a temporary directory, recorded in
# CYSU_TMP so cleanup removes it on exit. Git is told never to prompt, so a
# private repository without credentials fails at once instead of waiting
# for input the user cannot see.
skill_checkout() {
	if [ -d "$CYSU_SKILL_SOURCE" ]; then
		CYSU_SKILL_DIR="$CYSU_SKILL_SOURCE"
		return
	fi
	case "$CYSU_SKILL_SOURCE" in
	*://* | git@*) url="$CYSU_SKILL_SOURCE" ;;
	*) url="https://github.com/$CYSU_SKILL_SOURCE.git" ;;
	esac
	CYSU_TMP="$(mktemp -d)" || fail "could not create a temporary directory"
	GIT_TERMINAL_PROMPT=0 git clone -q --depth 1 "$url" "$CYSU_TMP/src" ||
		fail "could not clone $url; check that git can reach the repository (a private repository needs credentials, e.g. \`gh auth setup-git\`)"
	CYSU_SKILL_DIR="$CYSU_TMP/src"
}

# install_skill installs SKILL.md into the user's agents with the `skills` CLI.
# Agents come from CYSU_SKILL_AGENTS when set. Otherwise the installer's own
# picker asks which agents to use. It is given the terminal as stdin because
# under `curl | sh` stdin is the script itself, which would leave nothing to
# read the answer from. When no terminal can be opened the run is headless
# and every detected agent is used.
install_skill() {
	info "Installing the cysu skill from $CYSU_SKILL_SOURCE"
	skill_checkout
	set -- add "$CYSU_SKILL_DIR" -g
	if [ -n "$CYSU_SKILL_AGENTS" ]; then
		for agent in $CYSU_SKILL_AGENTS; do
			set -- "$@" -a "$agent"
		done
		npx -y skills "$@" -y || fail "skill installation failed"
	elif ( : </dev/tty ) 2>/dev/null; then
		npx -y skills "$@" </dev/tty || fail "skill installation failed"
	else
		npx -y skills "$@" -y || fail "skill installation failed"
	fi
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

# cleanup removes the temporary checkout, if one was made.
cleanup() {
	[ -n "${CYSU_TMP:-}" ] && rm -rf "$CYSU_TMP"
}

main() {
	need_cmd npm
	need_cmd npx
	need_cmd git
	CYSU_TMP=""
	trap cleanup EXIT
	install_binary
	check_path
	install_skill
	next_steps
}

main "$@"
