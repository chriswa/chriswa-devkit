#!/usr/bin/env bun

import { existsSync, readFileSync, writeFileSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'

const homeDir = homedir()
const zshrcPath = join(homeDir, '.zshrc')

// Find the devkit root directory (parent of install/)
const devkitPath = join(import.meta.dir, '..')
const sourceLine = `source ${devkitPath}/shell/index.sh`

function main() {
  try {
    process.stdout.write(`Working with file: ${zshrcPath}\n`)

    // Check if ~/.zshrc exists
    if (!existsSync(zshrcPath)) {
      process.stderr.write('Error: ~/.zshrc file not found\n')
      process.exit(1)
    }

    // Check if devkit directory exists
    if (!existsSync(devkitPath)) {
      process.stderr.write(`Error: devkit directory not found at ${devkitPath}\n`)
      process.exit(1)
    }

    // Read the current .zshrc content
    const zshrcContent = readFileSync(zshrcPath, 'utf8')

    // Check if the source line already exists
    if (zshrcContent.includes(sourceLine)) {
      process.stdout.write('chriswa-devkit shell config is already installed in ~/.zshrc\n')
      process.exit(0)
    }

    // Add the source line to the end of the file with proper spacing
    let newContent = zshrcContent

    // Ensure the file ends with a newline
    if (!newContent.endsWith('\n')) {
      newContent += '\n'
    }

    // Add blank line, source line, and final newline
    newContent += `\n${sourceLine}\n`

    // Write the updated content back to .zshrc
    writeFileSync(zshrcPath, newContent)

    process.stdout.write('Successfully added chriswa-devkit shell config to ~/.zshrc\n')
    process.stdout.write(`Added: ${sourceLine}\n`)
  }
  catch (error: unknown) {
    if (error instanceof Error) {
      process.stderr.write(`Error: ${error.message}\n`)
    }
    else {
      process.stderr.write(`Error: ${String(error)}\n`)
    }
    process.exit(1)
  }
}

main()
