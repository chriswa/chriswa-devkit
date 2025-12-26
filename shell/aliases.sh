#!/bin/bash

# Sleep management aliases
# this has been set in `/etc/sudoers.d/pmset`: `%admin ALL=(root) NOPASSWD: /usr/bin/pmset`
alias nosleep='sudo pmset -a disablesleep 1'
alias yessleep='sudo pmset -a disablesleep 0'
alias whatissleep='sudo pmset -g | grep SleepDisabled'

# Git aliases
alias g=git
alias gs='git status'
alias gc='git commit'
alias gb='git branch'

alias gitroot='git rev-parse --show-toplevel 2>/dev/null'

alias p=pnpm
