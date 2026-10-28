#!/usr/bin/env bash
# =============================================================================
# NextSQL Developer Environment Doctor
# Audits local toolchain, language runtimes, and developer dependencies.
# =============================================================================
set -euo pipefail

BOLD="\033[1m"
GREEN="\033[0;32m"
YELLOW="\033[0;33m"
RED="\033[0;31m"
BLUE="\033[0;34m"
RESET="\033[0m"

echo -e "${BOLD}${BLUE}NextSQL Development Environment Audit${RESET}\n"

check_cmd() {
  local name="$1"
  local cmd="$2"
  local required="$3"
  local ver_cmd="${4:-}"

  if command -v "$cmd" >/dev/null 2>&1; then
    local version=""
    if [ -n "$ver_cmd" ]; then
      version=$(eval "$ver_cmd" 2>&1 | head -n 1 || echo "")
    fi
    echo -e "  [${GREEN}OK${RESET}] ${BOLD}${name}${RESET} (${cmd}) ${version}"
    return 0
  else
    if [ "$required" = "true" ]; then
      echo -e "  [${RED}MISSING - REQUIRED${RESET}] ${BOLD}${name}${RESET} (${cmd})"
      return 1
    else
      echo -e "  [${YELLOW}OPTIONAL${RESET}] ${name} (${cmd}) not installed"
      return 0
    fi
  fi
}

ERRORS=0

echo -e "${BOLD}1. Core Toolchain (Required)${RESET}"
check_cmd "Go Compiler (1.24+)" "go" "true" "go version" || ERRORS=$((ERRORS + 1))
check_cmd "Git" "git" "true" "git --version" || ERRORS=$((ERRORS + 1))
check_cmd "GNU Make" "make" "true" "make --version" || ERRORS=$((ERRORS + 1))

echo -e "\n${BOLD}2. Web & Admin UI Toolchain${RESET}"
check_cmd "Node.js (v20+)" "node" "false" "node --version"
check_cmd "npm" "npm" "false" "npm --version"
check_cmd "Chrome / Chromium (a11y tests)" "${CHROME_BIN:-google-chrome}" "false" "${CHROME_BIN:-google-chrome} --version" || {
  check_cmd "Chromium fallback" "chromium" "false" "chromium --version" || true
}

echo -e "\n${BOLD}3. Official Driver Toolchains (Optional/Polyglot)${RESET}"
check_cmd "Bun (drivers/bun)" "bun" "false" "bun --version"
check_cmd "Python 3 (drivers/python)" "python3" "false" "python3 --version"
check_cmd "PHP (drivers/php)" "php" "false" "php --version"
check_cmd "Ruby (drivers/ruby)" "ruby" "false" "ruby --version"

echo -e "\n${BOLD}4. Container & Orchestration${RESET}"
check_cmd "Docker" "docker" "false" "docker --version"
check_cmd "Docker Compose" "docker" "false" "docker compose version"

echo -e "\n${BOLD}5. Repository Hygiene Check${RESET}"
DIRTY_COUNT=$(git status --porcelain 2>/dev/null | wc -l || echo "0")
if [ "$DIRTY_COUNT" -eq 0 ]; then
  echo -e "  [${GREEN}CLEAN${RESET}] Git working directory has no uncommitted changes."
else
  echo -e "  [${YELLOW}DIRTY${RESET}] Git working tree has ${DIRTY_COUNT} modified/untracked files."
fi

echo ""
if [ "$ERRORS" -gt 0 ]; then
  echo -e "${RED}${BOLD}Audit failed: ${ERRORS} required tool(s) missing.${RESET}"
  exit 1
else
  echo -e "${GREEN}${BOLD}Core development environment is ready!${RESET}"
fi
