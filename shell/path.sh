#!/bin/bash

# Add chriswa-devkit bin directory to PATH if not already present
# This avoids duplicate entries when sourcing multiple times

if [[ -n "$CHRISWA_DEVKIT_DIR" ]]; then
  CHRISWA_DEVKIT_BIN_DIR="$CHRISWA_DEVKIT_DIR/bin"

  # Check if the directory exists and is not already in PATH
  if [[ -d "$CHRISWA_DEVKIT_BIN_DIR" && ":$PATH:" != *":$CHRISWA_DEVKIT_BIN_DIR:"* ]]; then
    export PATH="$CHRISWA_DEVKIT_BIN_DIR:$PATH"
  fi
fi