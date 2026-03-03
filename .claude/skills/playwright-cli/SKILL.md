---
name: pw
description: Automates browser interactions for web testing, form filling, screenshots, and data extraction. Use when the user needs to navigate websites, interact with web pages, fill forms, take screenshots, test web applications, or extract information from web pages.
allowed-tools: Bash(pw), Bash(pw *)
---

# Browser Automation with pw

`pw` is a shared-browser wrapper. All commands use the same shared browser instance.

## Quick start

```bash
# open shared browser (idempotent)
pw open
# navigate to a page
pw goto https://playwright.dev
# interact with the page using refs from the snapshot
pw click e15
pw type "page.click"
pw press Enter
# take a screenshot
pw screenshot
# close current tab
pw close
# stop shared browser
pw stop
```

## pw wrapper behavior

- `pw open [url]`: if browser is already running, creates a new tab instead of restarting
- `pw close [index]`: closes current tab (or specified index), not the whole browser
- `pw stop`: stops the shared browser session
- `pw status`: show browser status and tab list
- `pw init-skill`: install this skill into current project's `.claude/skills/`
- All other commands forwarded transparently to `playwright-cli -s=shared`

## Commands

### Core

```bash
pw open
pw open https://example.com/
pw goto https://playwright.dev
pw type "search query"
pw click e3
pw dblclick e7
pw fill e5 "user@example.com"
pw drag e2 e8
pw hover e4
pw select e9 "option-value"
pw upload ./document.pdf
pw check e12
pw uncheck e12
pw snapshot
pw snapshot --filename=after-click.yaml
pw eval "document.title"
pw eval "el => el.textContent" e5
pw dialog-accept
pw dialog-accept "confirmation text"
pw dialog-dismiss
pw resize 1920 1080
pw close
```

### Navigation

```bash
pw go-back
pw go-forward
pw reload
```

### Keyboard

```bash
pw press Enter
pw press ArrowDown
pw keydown Shift
pw keyup Shift
```

### Mouse

```bash
pw mousemove 150 300
pw mousedown
pw mousedown right
pw mouseup
pw mouseup right
pw mousewheel 0 100
```

### Save as

```bash
pw screenshot
pw screenshot e5
pw screenshot --filename=page.png
pw pdf --filename=page.pdf
```

### Tabs

```bash
pw tab-list
pw tab-new
pw tab-new https://example.com/page
pw tab-close
pw tab-close 2
pw tab-select 0
```

### Storage

```bash
pw state-save
pw state-save auth.json
pw state-load auth.json

# Cookies
pw cookie-list
pw cookie-list --domain=example.com
pw cookie-get session_id
pw cookie-set session_id abc123
pw cookie-set session_id abc123 --domain=example.com --httpOnly --secure
pw cookie-delete session_id
pw cookie-clear

# LocalStorage
pw localstorage-list
pw localstorage-get theme
pw localstorage-set theme dark
pw localstorage-delete theme
pw localstorage-clear

# SessionStorage
pw sessionstorage-list
pw sessionstorage-get step
pw sessionstorage-set step 3
pw sessionstorage-delete step
pw sessionstorage-clear
```

### Network

```bash
pw route "**/*.jpg" --status=404
pw route "https://api.example.com/**" --body='{"mock": true}'
pw route-list
pw unroute "**/*.jpg"
pw unroute
```

### DevTools

```bash
pw console
pw console warning
pw network
pw run-code "async page => await page.context().grantPermissions(['geolocation'])"
pw tracing-start
pw tracing-stop
pw video-start
pw video-stop video.webm
```

## Snapshots

After each command, pw provides a snapshot of the current browser state.

```bash
> pw goto https://example.com
### Page
- Page URL: https://example.com/
- Page Title: Example Domain
### Snapshot
[Snapshot](.playwright-cli/page-2026-02-14T19-22-42-679Z.yml)
```

You can also take a snapshot on demand using `pw snapshot`.

If `--filename` is not provided, a new snapshot file is created with a timestamp. Use `--filename=` when the artifact is part of the workflow result.

## Example: Form submission

```bash
pw open https://example.com/form
pw snapshot

pw fill e1 "user@example.com"
pw fill e2 "password123"
pw click e3
pw snapshot
pw close
```

## Example: Multi-tab workflow

```bash
pw open https://example.com
pw tab-new https://example.com/other
pw tab-list
pw tab-select 0
pw snapshot
pw close
```

## Example: Debugging with DevTools

```bash
pw open https://example.com
pw click e4
pw fill e7 "test"
pw console
pw network
pw close
```

```bash
pw open https://example.com
pw tracing-start
pw click e4
pw fill e7 "test"
pw tracing-stop
pw close
```

## Specific tasks

* **Request mocking** [references/request-mocking.md](references/request-mocking.md)
* **Running Playwright code** [references/running-code.md](references/running-code.md)
* **Browser session management** [references/session-management.md](references/session-management.md)
* **Storage state (cookies, localStorage)** [references/storage-state.md](references/storage-state.md)
* **Test generation** [references/test-generation.md](references/test-generation.md)
* **Tracing** [references/tracing.md](references/tracing.md)
* **Video recording** [references/video-recording.md](references/video-recording.md)
