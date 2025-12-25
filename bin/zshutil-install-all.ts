#!/usr/bin/env bun

import { execSync } from 'child_process';
import { homedir } from 'os';
import { join } from 'path';

const homeDir = homedir();
const internalDir = join(homeDir, 'zshutil', 'bin', 'internal');

const scripts = [
  join(internalDir, 'zshutil-install-zshrc.ts'),
  join(internalDir, 'zshutil-install-claude-statusline.ts'),
  join(internalDir, 'zshutil-install-claude-hook.ts'),
];

try {
  for (const script of scripts) {
    execSync(script, { stdio: 'inherit' });
  }
} catch (error: any) {
  console.error('Installation failed:', error.message);
  process.exit(1);
}
