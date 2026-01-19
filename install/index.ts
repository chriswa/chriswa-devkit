#!/usr/bin/env bun

import { execSync } from 'child_process'
import { join } from 'path'

// Use import.meta.dir to find scripts relative to this file's location
const installDir = import.meta.dir
const repoRoot = join(installDir, '..')

const scripts = [
  join(installDir, 'shell.ts'),
  join(installDir, 'statusline.ts'),
]

try {
  // Make all scripts in default-plugin/scripts/ directory executable
  execSync(`chmod +x "${join(repoRoot, 'default-plugin/scripts')}"/*`, { stdio: 'inherit' })

  for (const script of scripts) {
    execSync(script, { stdio: 'inherit' })
  }
}
catch (error: unknown) {
  if (error instanceof Error) {
    process.stderr.write(`Installation failed: ${error.message}\n`)
  }
  else {
    process.stderr.write(`Installation failed: ${String(error)}\n`)
  }
  process.exit(1)
}
