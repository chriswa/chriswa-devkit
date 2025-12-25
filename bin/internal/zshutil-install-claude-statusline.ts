#!/usr/bin/env bun

import { execSync } from 'child_process'
import { copyFileSync, existsSync, readFileSync, writeFileSync } from 'fs'
import { homedir, tmpdir } from 'os'
import { basename, join } from 'path'
import { z } from 'zod'

const homeDir = homedir()
const claudeSettingsPath = join(homeDir, '.claude', 'settings.json')
const zshutilPath = join(homeDir, 'zshutil')
const claudeStatuslinePath = join(zshutilPath, 'bin', 'internal', 'claude', 'statusline.ts')

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
  console.log(`Created backup: ${backupPath}`)
  return backupPath
}

function displayStatusLineConfiguration(
  expectedStatusLine: { type: string, command: string },
  claudeSettingsPath: string,
): void {
  console.log(`Set statusLine.type: ${expectedStatusLine.type}`)
  console.log(`Set statusLine.command: ${expectedStatusLine.command}`)
  console.log('\nCurrent configuration in ~/.claude/settings.json:')

  try {
    const jqOutput = execSync(`jq '.statusLine' "${claudeSettingsPath}"`, {
      encoding: 'utf8',
      shell: '/bin/bash',
    })
    console.log(jqOutput)
  }
  catch {
    console.log('(Unable to display statusLine section with jq)')
  }
}

function main(): void {
  try {
    console.log(`Working with file: ${claudeSettingsPath}`)

    // Check if ~/.claude/settings.json exists
    if (!existsSync(claudeSettingsPath)) {
      console.error('Error: ~/.claude/settings.json file not found')
      process.exit(1)
    }

    // Check if ~/zshutil/bin/internal/claude/statusline exists
    if (!existsSync(claudeStatuslinePath)) {
      console.error('Error: ~/zshutil/bin/internal/claude/statusline not found')
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
      console.log('Claude statusline is already installed in ~/.claude/settings.json')
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

    console.log('Successfully configured Claude statusline in ~/.claude/settings.json')
    displayStatusLineConfiguration(expectedStatusLine, claudeSettingsPath)
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
