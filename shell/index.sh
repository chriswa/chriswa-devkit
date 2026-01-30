#!/bin/bash

# chriswa-devkit - Modular shell configuration and development utilities
# Source this file from your ~/.zshrc to load all utilities

# Get the directory where this script is located
# Use a more robust method that works in both bash and zsh
if [[ -n "${BASH_SOURCE[0]}" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
elif [[ -n "${(%):-%x}" ]]; then
    # zsh method
    SCRIPT_DIR="$(cd "$(dirname "${(%):-%x}")" && pwd)"
else
    # fallback - assume we're in the right directory
    SCRIPT_DIR="$HOME/chriswa-devkit/shell"
fi

CHRISWA_DEVKIT_DIR="$(dirname "$SCRIPT_DIR")"

source "$SCRIPT_DIR/path.sh"
source "$SCRIPT_DIR/aliases.sh"
source "$SCRIPT_DIR/killport.sh"
source "$SCRIPT_DIR/prompt.sh"
source "$SCRIPT_DIR/wt.sh"

export CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1
