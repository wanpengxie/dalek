# Browser Session Management

Run multiple isolated browser sessions concurrently with state persistence.

## Named Browser Sessions

Use `-s` flag to isolate browser contexts:

```bash
# Browser 1: Authentication flow
pw -s=auth open https://app.example.com/login

# Browser 2: Public browsing (separate cookies, storage)
pw -s=public open https://example.com

# Commands are isolated by browser session
pw -s=auth fill e1 "user@example.com"
pw -s=public snapshot
```

## Browser Session Isolation Properties

Each browser session has independent:
- Cookies
- LocalStorage / SessionStorage
- IndexedDB
- Cache
- Browsing history
- Open tabs

## Browser Session Commands

```bash
# List all browser sessions
pw list

# Stop a browser session (close the browser)
pw close                # stop the default browser
pw -s=mysession close   # stop a named browser

# Stop all browser sessions
pw close-all

# Forcefully kill all daemon processes (for stale/zombie processes)
pw kill-all

# Delete browser session user data (profile directory)
pw delete-data                # delete default browser data
pw -s=mysession delete-data   # delete named browser data
```

## Environment Variable

Set a default browser session name via environment variable:

```bash
export PLAYWRIGHT_CLI_SESSION="mysession"
pw open example.com  # Uses "mysession" automatically
```

## Common Patterns

### Concurrent Scraping

```bash
#!/bin/bash
# Scrape multiple sites concurrently

# Start all browsers
pw -s=site1 open https://site1.com &
pw -s=site2 open https://site2.com &
pw -s=site3 open https://site3.com &
wait

# Take snapshots from each
pw -s=site1 snapshot
pw -s=site2 snapshot
pw -s=site3 snapshot

# Cleanup
pw close-all
```

### A/B Testing Sessions

```bash
# Test different user experiences
pw -s=variant-a open "https://app.com?variant=a"
pw -s=variant-b open "https://app.com?variant=b"

# Compare
pw -s=variant-a screenshot
pw -s=variant-b screenshot
```

### Persistent Profile

By default, browser profile is kept in memory only. Use `--persistent` flag on `open` to persist the browser profile to disk:

```bash
# Use persistent profile (auto-generated location)
pw open https://example.com --persistent

# Use persistent profile with custom directory
pw open https://example.com --profile=/path/to/profile
```

## Default Browser Session

When `-s` is omitted, commands use the default browser session:

```bash
# These use the same default browser session
pw open https://example.com
pw snapshot
pw close  # Stops default browser
```

## Browser Session Configuration

Configure a browser session with specific settings when opening:

```bash
# Open with config file
pw open https://example.com --config=.playwright/my-cli.json

# Open with specific browser
pw open https://example.com --browser=firefox

# Open in headed mode
pw open https://example.com --headed

# Open with persistent profile
pw open https://example.com --persistent
```

## Best Practices

### 1. Name Browser Sessions Semantically

```bash
# GOOD: Clear purpose
pw -s=github-auth open https://github.com
pw -s=docs-scrape open https://docs.example.com

# AVOID: Generic names
pw -s=s1 open https://github.com
```

### 2. Always Clean Up

```bash
# Stop browsers when done
pw -s=auth close
pw -s=scrape close

# Or stop all at once
pw close-all

# If browsers become unresponsive or zombie processes remain
pw kill-all
```

### 3. Delete Stale Browser Data

```bash
# Remove old browser data to free disk space
pw -s=oldsession delete-data
```
