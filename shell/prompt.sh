#!/bin/bash

### PROMPT

# Get Git branch name (fast)
function git_branch() {
  git rev-parse --abbrev-ref HEAD 2>/dev/null
}

# Check for dirty/staged files (fast)
function git_status_color() {
  git rev-parse --is-inside-work-tree &>/dev/null || return

  git diff --quiet --ignore-submodules HEAD || return 1  # dirty
  git diff --cached --quiet || return 2                  # staged but clean
  return 0                                               # clean
}

function set_prompt() {
  local exit_code=$?

  local esc=$'\e'
  local reset="%{${esc}[0m%}"
  local colour_cwd="%{${esc}[30;107m%}"
  local colour_good_exit="%{${esc}[30;107m%}"
  local colour_bad_exit="%{${esc}[93;41m%}"

  local bright_white_on_black="%{${esc}[97;40m%}"

  local colour_branch_clean="%{${esc}[30;106m%}"
  local colour_branch_staged="%{${esc}[30;103m%}"
  local colour_branch_dirty="%{${esc}[30;105m%}"

  local branch_color=""
  local branch_name=$(git_branch)

  if [[ -n "$branch_name" ]]; then
    git_status_color
    local git_state=$?

    if [[ $git_state -eq 1 ]]; then
      branch_color=$colour_branch_dirty
    elif [[ $git_state -eq 2 ]]; then
      branch_color=$colour_branch_staged
    else
      branch_color=$colour_branch_clean
    fi

    branch_name="${branch_color} ${branch_name} ${reset}"
  fi

  local cwd='%~'

  local exit_colour="${colour_good_exit}"
  if [[ $exit_code -ne 0 ]]; then
    exit_colour="${colour_bad_exit}"
  fi
  PROMPT="${branch_name}${exit_colour} ${cwd} ${reset}${bright_white_on_black} "
}

# Set up the prompt hook
autoload -Uz add-zsh-hook
add-zsh-hook precmd set_prompt