// The dialog for agent-dead-drop: shows a message and a wide, wrapping text field,
// and prints what the user entered to stdout (no trailing newline).
//
// Usage: dead-drop-dialog <message> <timeout-seconds>
// Exit codes: 0 OK pressed, 1 Cancel pressed or timed out, 2 usage error.
import AppKit

let args = CommandLine.arguments
guard args.count == 3, let timeout = TimeInterval(args[2]) else {
    FileHandle.standardError.write(Data("usage: dead-drop-dialog <message> <timeout-seconds>\n".utf8))
    exit(2)
}

/// Key equivalents like ⌘V and ⌘A are dispatched through the main menu, so without
/// an Edit menu the field can't paste or select all. Accessory apps never show it.
func editMenu() -> NSMenu {
    let edit = NSMenu(title: "Edit")
    edit.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
    edit.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
    edit.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
    edit.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
    edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
    edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
    let item = NSMenuItem()
    item.submenu = edit
    let main = NSMenu()
    main.addItem(item)
    return main
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
app.mainMenu = editMenu()

let alert = NSAlert()
alert.messageText = args[1]
alert.informativeText = "Paste the value and press Return. The agent only receives a file path."
alert.addButton(withTitle: "OK")
alert.addButton(withTitle: "Cancel")

let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 460, height: 100))
field.usesSingleLineMode = false
field.cell?.wraps = true
field.cell?.isScrollable = false
field.lineBreakMode = .byCharWrapping
field.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
alert.accessoryView = field
alert.window.initialFirstResponder = field
alert.layout()
alert.window.level = .floating

let timer = Timer(timeInterval: timeout, repeats: false) { _ in NSApp.abortModal() }
RunLoop.main.add(timer, forMode: .modalPanel)

app.activate(ignoringOtherApps: true)
guard alert.runModal() == .alertFirstButtonReturn else { exit(1) }
print(field.stringValue, terminator: "")
exit(0)
