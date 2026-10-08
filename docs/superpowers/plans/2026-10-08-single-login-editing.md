# Single-Login Editing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let authenticated users edit immediately without entering the password a second time.

**Architecture:** Authentication remains session-based and is the only server-side authorization gate. Vue keeps a local `editing` presentation state, while every mutation endpoint accepts any valid logged-in session and retains existing CSRF and validation checks.

**Tech Stack:** Go 1.23 `net/http`, Vue 3 local runtime, Go `testing`, GitHub Actions/GHCR.

---

### Task 1: Make Login the Only Write Authorization Gate

**Files:**
- Modify: `main_test.go`
- Modify: `main.go`

- [ ] **Step 1: Write the failing tests**

Change `TestPageAuthEditAndPersistence` so the first authenticated `PUT /api/data` must return `200` without calling `/api/edit/unlock`. Keep the anonymous request assertion and revision-conflict assertion. Remove edit lock/unlock requests from background and duplicate-save tests.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./... -run 'TestPageAuthEditAndPersistence|TestBackgroundUploadServeAndClear|TestAPIServerRejectsDuplicateSave'`

Expected: `TestPageAuthEditAndPersistence` fails because the current server returns `403` before an edit unlock.

- [ ] **Step 3: Implement the minimal backend change**

In `main.go`, reduce `Session` to the login expiry, remove `editTTL`, remove `/api/edit/unlock` and `/api/edit/lock`, return only `loggedIn` from `/api/me`, and remove `EditUntil` checks from `PUT /api/data`, `POST /api/background`, and `DELETE /api/background`.

- [ ] **Step 4: Run focused and full tests**

Run: `go test ./... -run 'TestPageAuthEditAndPersistence|TestBackgroundUploadServeAndClear|TestAPIServerRejectsDuplicateSave'`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

### Task 2: Remove the Secondary Password UI

**Files:**
- Modify: `web/app.js`
- Modify: `web/index.html`

- [ ] **Step 1: Add a failing source-level regression check**

Run a source scan that fails while any obsolete token remains:

```bash
rg -n "editPassword|modal === 'unlock'|modal==='unlock'|/api/edit/(unlock|lock)|编辑密码|再次输入密码|解锁编辑" web main.go
```

Expected: matches in `web/app.js` and `web/index.html`.

- [ ] **Step 2: Implement direct edit toggling**

Remove `editPassword`, unlock modal title/content/submission, and edit lock API calls. Make `toggleEditing` wait for queued saves when leaving edit mode, then flip `editing`; entering edit mode sets it immediately. Do not restore editing from `/api/me`.

- [ ] **Step 3: Verify obsolete flows are gone**

Run the source scan from Step 1.

Expected: exit status 1 with no matches.

### Task 3: Update Documentation and Screenshots

**Files:**
- Modify: `README.md`
- Add: `docs/images/navigation-edit.jpg`
- Add: `docs/images/login.jpg`

- [ ] **Step 1: Document the new behavior**

State that one login grants editing access, remove references to edit unlock expiry and repeated password entry, and keep the screenshot preview section near the top.

- [ ] **Step 2: Validate documentation and assets**

Run: `test -s docs/images/navigation-edit.jpg && test -s docs/images/login.jpg`

Expected: exit 0.

Run: `rg -n "再次输入密码|编辑解锁|编辑授权" README.md`

Expected: exit status 1 with no matches.

### Task 4: Release v1.2

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add v1.2 release notes**

Add a dated section covering single-login editing, custom background support, UI fixes, README screenshots, and the tag-triggered Docker package workflow.

- [ ] **Step 2: Verify the release tree**

Run: `go test ./...`

Expected: PASS.

Run: `git diff --check`

Expected: no output.

- [ ] **Step 3: Commit, push, and tag**

Commit implementation and documentation, push `main`, create annotated tag `v1.2` from the new changelog section, and push the tag.

- [ ] **Step 4: Verify GitHub state**

Confirm `origin/main`, remote `refs/tags/v1.2`, public repository visibility, and a successful Docker Package workflow run for `v1.2`.
