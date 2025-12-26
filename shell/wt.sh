#!/bin/bash

# Create a new git worktree (auto-detects new vs existing branch)
# Usage: wt mot-123
wt() {
  local branch_name=$1

  if [[ -z "$branch_name" ]]; then
    echo "Usage: wt <branch-name>"
    echo "Example: wt mot-123"
    return 1
  fi

  # Get the git repo root and name
  local repo_path=$(git rev-parse --show-toplevel 2>/dev/null)
  if [[ -z "$repo_path" ]]; then
    echo "Error: Not in a git repository"
    return 1
  fi

  # Determine repo name: if already in ~/wt/, use that structure
  local repo_name
  local expanded_wt_path="${HOME}/wt"
  if [[ "$PWD" == "$expanded_wt_path"/* ]]; then
    # We're in a wt directory, extract the repo name from the path
    local relative_path="${PWD#$expanded_wt_path/}"
    repo_name="${relative_path%%/*}"
  else
    # Not in a wt directory, use repo root basename
    repo_name=$(basename "$repo_path")
  fi

  local worktree_path=~/wt/$repo_name/$branch_name

  echo "Creating worktree at $worktree_path..."

  # Create parent directory structure
  mkdir -p ~/wt/$repo_name

  # Fetch latest from origin
  git -C "$repo_path" fetch origin || return 1

  # Detect the default branch (main, master, etc.)
  local default_branch=$(git -C "$repo_path" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
  if [[ -z "$default_branch" ]]; then
    # Fallback: try common default branch names
    if git -C "$repo_path" show-ref --verify --quiet refs/remotes/origin/main; then
      default_branch="main"
    elif git -C "$repo_path" show-ref --verify --quiet refs/remotes/origin/master; then
      default_branch="master"
    else
      echo "Error: Could not determine default branch"
      return 1
    fi
  fi

  # Check if branch exists locally or remotely
  if git -C "$repo_path" show-ref --verify --quiet refs/heads/$branch_name || \
     git -C "$repo_path" show-ref --verify --quiet refs/remotes/origin/$branch_name; then
    echo "✓ Checking out existing branch '$branch_name'"
    git -C "$repo_path" worktree add "$worktree_path" "$branch_name" || return 1
  else
    echo "✓ Creating new branch '$branch_name' from origin/$default_branch"
    git -C "$repo_path" worktree add "$worktree_path" -b "$branch_name" "origin/$default_branch" || return 1
  fi

  # Change into the new worktree
  cd "$worktree_path"
}
