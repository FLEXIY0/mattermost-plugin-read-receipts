# Message Status — Mattermost Plugin

Telegram-style read receipts for your own messages.

| Ticks | Meaning |
| :---: | ------- |
| ✓ | **Delivered** — the message is stored on the server |
| ✓✓ | **Read** — someone opened it in the web client |

Both states are drawn in the same muted grey and follow the active Mattermost
theme (light, dark or custom) — the signal is one tick vs. two, not a colour
change. Ticks appear **only on posts you authored**, in the bottom-right corner
of the message.

> **Disclaimer:** built with AI assistance and provided as-is, without warranty.
> Review the code and test it in a staging environment before production use.

## Example

Send a message in a DM or channel:

```
You:  Deploy is done, can you take a look?     ✓
```

The single grey tick appears as soon as the server has stored the post. Once
the recipient scrolls it into view in the web client, it becomes two ticks:

```
You:  Deploy is done, can you take a look?     ✓✓
```

Hover the ticks to see who has read the message.

<!-- Screenshots -->
<!-- ![Delivered vs read](images/delivered-vs-read.png) -->
<!-- ![Read-by tooltip](images/read-by-tooltip.png) -->

## Install

Build the bundle, then upload it:

```bash
make dist
mmctl plugin upload dist/com.github.mattermost-message-status-1.3.0.tar.gz
mmctl plugin enable com.github.mattermost-message-status
```

Or go to **System Console → Plugins → Plugin Management**, upload the same
file and enable **Message Status**. Reload the web client afterwards
(**Ctrl+F5**).

## Settings

**System Console → Plugins → Message Status** has one setting:

- **Checkmark size (pixels)** — height of the ticks, 6–20, default 11. Spacing
  scales with it, and the stroke thickens at the smallest sizes so the glyph
  stays legible. Values outside the range fall back to the default. Users pick
  up a change on their next reload.

On a post that ends in an image or a link preview the ticks sit on top of that
picture, so there they are drawn white on a dark rounded scrim instead of in the
theme colour — a theme-coloured tick is invisible over a photo of the wrong
tone.

`make dist` builds a Linux-only bundle. Use `make dist-all` for a bundle that
also carries the macOS and Windows binaries (larger; may need a higher
`FileSettings.MaxFileSize`).

## Requirements

- Mattermost **9.0+** (tested with 10.x)
- Go **1.24+** and Node.js **18+** to build

## What it does and does not track

- **Delivered** is set by a server hook, so it is accurate for every client.
- **Read in a direct message** works from any client, including the mobile apps.
  Those never load webapp plugins, so instead the server compares the
  recipient's channel view time against the post. The author sees the second
  tick on their next refresh (window focus, reconnect or channel switch) rather
  than instantly, and it means "opened the conversation after this message
  arrived" rather than "scrolled this exact message into view".
- **Read in a channel** is detected only in the **web client**, where the
  recipient actually scrolls the post into view. If your team reads mostly on
  phones, a single tick on a channel post does not mean unread.
- System messages, deleted posts, and webhook/bot posts are ignored.
- Receipts are stored for **30 days**, then expire; a post older than that
  falls back to showing **Delivered**.
- A post records at most **50** distinct readers.

## Privacy

Read receipts tell the author who opened their message. Only the author can see
them: the server returns a post's reader list solely to the account that wrote
the post, and pushes updates over a websocket addressed to that user alone.
Receipts are deleted when the post is deleted. There is no admin view and no
way to query someone else's receipts.

## Development

```bash
make check    # go vet + go test + webapp typecheck
make dist     # full build + bundle
make clean    # remove build artifacts
```

Run `make check` before committing — the webpack build strips TypeScript types
without checking them, so `check-types` is the only thing that validates the
webapp.

To cut a release, bump the version in `plugin.json`, `plugin.full.json`,
`Makefile` and `webapp/src/manifest.ts`, then either:

- push a tag — `git tag v1.3.0 && git push origin v1.3.0`, or
- use **Releases → Draft a new release** on GitHub and create the tag there.

Either way CI verifies that all four files agree with the tag, runs the checks,
and attaches the bundles with checksums to the release.

See [CLAUDE.md](CLAUDE.md) for the architecture notes.

## License

MIT — see [LICENSE](LICENSE).
