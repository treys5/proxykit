# Building Proxy Tester

Proxy Tester is an Electron desktop app.  
Building it produces a **self-contained package** — no Node.js, no installs, no dependencies needed on the end-user machine.

---

## Prerequisites (one-time setup for the builder)

| Platform | Requirement |
|----------|-------------|
| **Windows** | [Node.js 18 LTS](https://nodejs.org) (or use the bundled `node/node.exe`) |
| **Mac** | [Node.js 18 LTS](https://nodejs.org) |
| **Both** | Internet access on first build (downloads the Electron binary ~100 MB) |

---

## Windows → ZIP

The Windows build produces a **ZIP file** containing the full self-contained app.  
Users just unzip and double-click `Proxy Tester.exe` — no installer required.

**Steps:**

1. Open **PowerShell** in the project folder.
2. Run:
   ```powershell
   .\build.ps1
   ```
3. When done, find the output in **`dist\`**:
   ```
   dist\Proxy Tester-win32-x64.zip   ← share this
   ```
4. Share the ZIP. Users unzip it anywhere and run `Proxy Tester.exe`.

### Options
| Flag | Effect |
|------|--------|
| `-SkipInstall` | Skip `npm install` (faster on subsequent builds) |
| `-NoZip` | Skip ZIP creation; leave the raw app folder in `dist\` |

---

## Mac → DMG

> **Must be run on a Mac.**  
> Cross-compiling Mac apps from Windows is not supported by Apple.

1. Open **Terminal** in the project folder.
2. Make the script executable (first time only):
   ```bash
   chmod +x build.sh
   ```
3. Run:
   ```bash
   ./build.sh
   ```
4. When done, find the output in **`dist/`**:
   ```
   dist/Proxy Tester-1.05.0.dmg   ← share this
   ```
5. Users double-click the DMG, drag **Proxy Tester** into Applications, done.

### Gatekeeper / "Unidentified Developer" warning

Because the app is not notarized by Apple, macOS may show a warning on first launch.

To open it anyway:
- **Right-click** the app in Finder → **Open** → click **Open** in the dialog.

Or run this in Terminal after copying to Applications:
```bash
xattr -cr "/Applications/Proxy Tester.app"
```

---

## Custom Icons (optional)

Place icon files in the `assets/` folder before building.  
If absent, Electron's default icon is used automatically.

| File | Platform | Size |
|------|----------|------|
| `assets/icon.ico` | Windows | 256×256 `.ico` |
| `assets/icon.icns` | Mac | 512×512 `.icns` |
| `assets/icon.png` | Linux | 512×512 `.png` |

To enable custom icons on Windows, add this to the `"win"` section in `package.json`:
```json
"icon": "assets/icon.ico"
```

---

## Output structure

```
dist/
  Proxy Tester-win32-x64.zip        ← Windows: share this ZIP
  Proxy Tester-win32-x64/           ← Windows: raw app folder
    Proxy Tester.exe
    resources/
    ...
  Proxy Tester-1.05.0.dmg           ← Mac: share this DMG (built on Mac)
```

---

## Troubleshooting

**`npm install` fails with network errors**  
electron-packager/builder downloads the Electron binary (~80 MB) from GitHub on first build. Use a stable internet connection.

**`electron-packager` / `electron-builder` command not found**  
Run `npm install` first. These binaries live in `node_modules/.bin/`.

**Mac: "App can't be opened because it is from an unidentified developer"**  
Use the right-click → Open method or the `xattr -cr` command above.

**Windows: ZIP is very large (150+ MB)**  
This is normal — Electron embeds its own Chromium runtime. The installed size is ~200 MB, similar to Chrome or VS Code.
