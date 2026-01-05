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
const hookScriptPath = join(devkitPath, 'claude', 'hook.ts')
const hookCommand = `${hookScriptPath} bash PreToolUse`

const expectedHook = {
  matcher: 'Bash',
  hooks: [
    {
      type: 'command',
      command: hookCommand,
    },
  ],
}

// Zod schemas for Claude settings.json hooks
const HookSchema = z.object({
  type: z.string(),
  command: z.string(),
})

const PreToolUseHookSchema = z.object({
  matcher: z.string(),
  hooks: z.array(HookSchema).optional(),
})

const SettingsSchema = z.object({
  hooks: z.object({
    PreToolUse: z.array(PreToolUseHookSchema).optional(),
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

function displayHookConfiguration(hookCommand: string, claudeSettingsPath: string): void {
  process.stdout.write(`Hook command: ${hookCommand}\n`)
  process.stdout.write('\nCurrent configuration in ~/.claude/settings.json:\n')

  try {
    const jqOutput = execSync(`jq '.hooks.PreToolUse' "${claudeSettingsPath}"`, {
      encoding: 'utf8',
      shell: '/bin/bash',
    })
    process.stdout.write(jqOutput)
  }
  catch {
    process.stdout.write('(Unable to display hooks section with jq)\n')
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

    // Check if hook script exists
    if (!existsSync(hookScriptPath)) {
      process.stderr.write(`Error: ${hookScriptPath} not found\n`)
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

    // Initialize hooks object if it doesn't exist
    settings.hooks ??= {}

    // Initialize PreToolUse array if it doesn't exist
    settings.hooks.PreToolUse ??= []

    // Check if a Bash hook already exists
    const existingBashHookIndex = settings.hooks.PreToolUse.findIndex(
      (hook) => hook.matcher === 'Bash',
    )

    if (existingBashHookIndex !== -1) {
      // Check if our Bash PreToolUse guard is already in the hooks
      const existingHooks = settings.hooks.PreToolUse[existingBashHookIndex].hooks ?? []
      const alreadyInstalled = existingHooks.some(
        (h) => h.type === 'command' && h.command === hookCommand,
      )

      if (alreadyInstalled) {
        process.stdout.write('Bash PreToolUse guard hook is already installed in ~/.claude/settings.json\n')
        displayHookConfiguration(hookCommand, claudeSettingsPath)
        return
      }

      // Add our hook to the existing Bash matcher
      existingHooks.push({
        type: 'command',
        command: hookCommand,
      })
      settings.hooks.PreToolUse[existingBashHookIndex].hooks = existingHooks
      process.stdout.write('Added Bash PreToolUse guard to existing Bash hooks\n')
    }
    else {
      // No Bash hook exists, add our complete hook configuration
      settings.hooks.PreToolUse.push(expectedHook)
      process.stdout.write('Created new Bash hook with PreToolUse guard\n')
    }

    // Write the updated settings back to file with pretty formatting
    const updatedContent = `${JSON.stringify(settings, null, 2)}\n`
    writeFileSync(claudeSettingsPath, updatedContent)

    process.stdout.write('Successfully configured Bash PreToolUse guard hook in ~/.claude/settings.json\n')
    displayHookConfiguration(hookCommand, claudeSettingsPath)
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
