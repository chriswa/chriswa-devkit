declare module 'claude-code' {
  interface PluginState {
    'mute-tui': {
      // Plain-text replies are being discarded in this session.
      muted: boolean
      // A reply was discarded and Claude has not been told yet.
      pending: boolean
    }
  }
}
