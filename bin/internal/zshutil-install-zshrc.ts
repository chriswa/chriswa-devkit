#!/usr/bin/env bun

import { existsSync, readFileSync, writeFileSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'

const homeDir = homedir()
const zshrcPath = join(homeDir, '.zshrc')
const zshutilPath = join(homeDir, 'zshutil')
const sourceLine = `source ${zshutilPath}/zshrc/index.sh`

function main() {
  try {
    console.log(`Working with file: ${zshrcPath}`)

    // Check if ~/.zshrc exists
    if (!existsSync(zshrcPath)) {
      console.error('Error: ~/.zshrc file not found')
      process.exit(1)
    }

    // Check if ~/zshutil exists
    if (!existsSync(zshutilPath)) {
      console.error('Error: ~/zshutil directory not found')
      process.exit(1)
    }

    // Read the current .zshrc content
    const zshrcContent = readFileSync(zshrcPath, 'utf8')

    // Check if the source line already exists
    if (zshrcContent.includes(sourceLine)) {
      console.log('zshutil is already installed in ~/.zshrc')
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

    console.log('Successfully added zshutil source line to ~/.zshrc')
    console.log(`Added: ${sourceLine}`)
  }
  catch (error: unknown) {
    if (error instanceof Error) {
      console.error('Error:', error.message)
    }
    else {
      console.error('Error:', error)
    }
    process.exit(1)
  }
}

main()
