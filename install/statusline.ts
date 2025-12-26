#!/usr/bin/env bun

import { execSync } from 'child_process'
import { copyFileSync, existsSync, readFileSync, writeFileSync } from 'fs'
import { homedir, tmpdir } from 'os'
import { basename, join } from 'path'
import { z } from 'zod'

const homeDir = homedir()
const claudeSettingsPath = join(homeDir, '.claude', 'settings.json')

// Find the devkit root directory (parent of install/)
const devkitPath = join(import.meta.dir, '..')
const claudeStatuslinePath = join(devkitPath, 'claude', 'statusline', 'index.ts')

const expectedStatusLine = {
  type: 'command',
  command: claudeStatuslinePath,
}

// Zod schema for Claude settings.json
const SettingsSchema = z.object({
  statusLine: z.object({
    type: z.string().optional(),
    command: z.string().optional(),
  }).optional(),
}).passthrough() // Allow other properties

type Settings = z.infer<typeof SettingsSchema>

function createBackup(filePath: string): string {
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
  const fileName = basename(filePath)
  const backupPath = join(tmpdir(), `${fileName}.backup.${timestamp}`)

  copyFileSync(filePath, backupPath)
  process.stdout.write(`Created backup: ${backupPath}\n`)
  return backupPath
}

function displayStatusLineConfiguration(
  expectedStatusLine: { type: string, command: string },
  claudeSettingsPath: string,
): void {
  process.stdout.write(`Set statusLine.type: ${expectedStatusLine.type}\n`)
  process.stdout.write(`Set statusLine.command: ${expectedStatusLine.command}\n`)
  process.stdout.write('\nCurrent configuration in ~/.claude/settings.json:\n')

  try {
    const jqOutput = execSync(`jq '.statusLine' "${claudeSettingsPath}"`, {
      encoding: 'utf8',
      shell: '/bin/bash',
    })
    process.stdout.write(jqOutput)
  }
  catch {
    process.stdout.write('(Unable to display statusLine section with jq)\n')
  }
}

function main(): void {
  try {
    process.stdout.write(`Working with file: ${claudeSettingsPath}\n`)

    // Check if ~/.claude/settings.json exists
    if (!existsSync(claudeSettingsPath)) {
      process.stderr.write('Error: ~/.claude/settings.json file not found\n')
      process.exit(1)
    }

    // Check if statusline script exists
    if (!existsSync(claudeStatuslinePath)) {
      process.stderr.write(`Error: ${claudeStatuslinePath} not found\n`)
      process.exit(1)
    }

    // Create backup before making changes
    createBackup(claudeSettingsPath)

    // Read and parse the current settings.json
    const settingsContent = readFileSync(claudeSettingsPath, 'utf8')
    const parseResult = SettingsSchema.safeParse(JSON.parse(settingsContent))

    if (!parseResult.success) {
      console.error('Error: Invalid JSON in ~/.claude/settings.json')
      console.error(parseResult.error)
      process.exit(1)
    }

    const settings: Settings = parseResult.data

    // Check if statusLine already exists and matches expected values
    if (
      settings.statusLine
      && settings.statusLine.type === expectedStatusLine.type
      && settings.statusLine.command === expectedStatusLine.command
    ) {
      process.stdout.write('Claude statusline is already installed in ~/.claude/settings.json\n')
      displayStatusLineConfiguration(expectedStatusLine, claudeSettingsPath)
      return
    }

    // Initialize statusLine property if it doesn't exist
    settings.statusLine ??= {}

    // Set the required properties
    settings.statusLine.type = expectedStatusLine.type
    settings.statusLine.command = expectedStatusLine.command

    // Write the updated settings back to file with pretty formatting
    const updatedContent = `${JSON.stringify(settings, null, 2)}\n`
    writeFileSync(claudeSettingsPath, updatedContent)

    process.stdout.write('Successfully configured Claude statusline in ~/.claude/settings.json\n')
    displayStatusLineConfiguration(expectedStatusLine, claudeSettingsPath)
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
