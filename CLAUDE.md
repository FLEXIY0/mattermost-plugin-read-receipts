# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Mattermost plugin (`com.github.mattermost-message-status`) that renders
Telegram-style delivery/read ticks on the author's own posts. It has a Go server
component and a React/TypeScript webapp component that ship in one bundle.

## Commands

```bash
make check            # go vet + go test + webapp typecheck — run before committing
make dist             # linux bundle: server-linux + webapp + bundle
make dist-all         # bundle for linux + darwin + windows
make clean            # drop dist/, server/dist/, webapp/dist/, node_modules/

cd server && go test ./...                      # all server tests
cd server && go test -run TestIsTrackablePost ./...   # one test
cd webapp && npm run check-types                 # tsc --noEmit
cd webapp && npm run debug                       # unminified webapp build
```

`npm run build` uses **babel-loader**, which strips TypeScript types without
checking them. A type error will build and ship silently — `npm run check-types`
(via `make check`) is the only thing that validates the webapp.

Building the bundle uses `build/bundle.go`, a hand-rolled tar writer, rather
than the upstream `mattermost-plugin-starter-template` tooling. It exists
because the bundle needs a USTAR layout with `0755` on `server/dist/plugin-*`;
don't replace it with `tar` shell-outs.

## Version bumps

The version is duplicated in four places and all of them must move together, or
Mattermost will refuse the upgrade or serve a stale webapp bundle:

- `plugin.json` and `plugin.full.json` (`version`)
- `Makefile` (`PLUGIN_VERSION`, which names the bundle file)
- `webapp/src/manifest.ts` (`version`)

The plugin **ID** is duplicated in `plugin.json`, `plugin.full.json`,
`Makefile`, `webapp/src/manifest.ts` and `webapp/src/types/store.ts`
(`PLUGIN_STATE_KEY`, which also derives the websocket event name).

## Releases

`.github/workflows/release.yml` fires on a `v*` tag. It re-checks that all four
version declarations equal the tag (minus the `v`) and **fails the release** if
any of them drifted, so the bump above is enforced rather than remembered. It
then runs `make check`, builds both bundles and publishes them.

The two bundles are built from the same `dist/` filename, so the workflow moves
the first aside before building the second:

- `<id>-<version>-linux.tar.gz` — `make dist`, ships `plugin.json`, linux only
- `<id>-<version>.tar.gz` — `make dist-all`, ships `plugin.full.json`, all platforms

A tag containing a hyphen (`v1.2.0-rc1`) publishes as a prerelease. Running the
workflow manually (`workflow_dispatch`) builds and uploads the bundles as
workflow artifacts without publishing anything.

## Architecture

### Data flow

1. `MessageHasBeenPosted` (server) writes a `PostStatus` to the plugin KV store
   with `Delivered: true` and publishes `status_updated` to the author.
2. The recipient's webapp sees the post enter the viewport, `POST /api/v1/read`.
3. The server appends the reader to `ReadBy` and publishes `status_updated`
   to the **author only**.
4. The author's webapp reducer flips the post to `read`, and the portal
   re-renders two ticks.

On reconnect/focus/channel switch the webapp re-hydrates via
`GET /api/v1/status?post_ids=...` because websocket events are lossy.

That flow only exists in the webapp, so a recipient on the mobile app never
reports a read. `dmread.go` closes that gap for **direct messages only**: on a
status request the server compares the other participant's
`ChannelMember.LastViewedAt` against the post's `CreatedAt`, which every client
updates because it drives the unread badges. There is no hook for it, so it is
pull-based and the author sees the tick on their next hydration. `dmViewLookup`
caches the channel and member lookups per request — without it a screenful of
one conversation would be two API calls per post. It is confined to DMs on
purpose: in a channel it would mean loading every member on every request, and
"opened the channel" says much less about a specific message.

### Server (`server/`)

- `plugin.go` — hooks, KV persistence, `markDelivered`/`markRead`, websocket publish
- `http.go` — router, auth middleware, the two HTTP handlers
- `api.go` — `PostStatus` model and its invariants (`addReader`, `DerivedStatus`)
- `dmread.go` — inferring DM reads from channel view times, for mobile clients

Persistence is one KV entry per post, keyed `status_<postID>`, written with
`KVSetWithOptions{Atomic: true, OldValue: ..., ExpireInSeconds: ...}`. **All
writes are compare-and-set**: an in-process mutex would not be correct because
Mattermost runs one plugin process per cluster node, and both `markDelivered`
and `markRead` can touch the same post concurrently from different nodes.
`getStatus` therefore returns the raw bytes alongside the decoded struct — the
raw value is the CAS token for the next write. `appendReader` retries the read/
modify/write loop `casAttempts` times and is shared by `markRead` and the
direct-message sync.

`MessageHasBeenPosted` runs for **every post on the server**, so it must stay
cheap: it tries an insert-only CAS first and only falls back to a read when that
loses. Keep it that way; adding an unconditional `KVGet` there doubles the
storage traffic of the whole instance.

### Webapp (`webapp/src/`)

Mattermost gives plugins no post-footer slot, so ticks are **portalled into the
DOM of the host app**:

- `MessageStatusPortals` finds `.post__body` / `.post__content` for each of the
  user's own visible posts, appends a host `div`, and `createPortal`s a
  `MessageStatusAttachment` into it. Hosts are cached in a module-level
  `portalHosts` map that must be pruned as posts leave the store, or it pins
  detached DOM nodes for the tab's lifetime.
- `PostReadTracker` runs an `IntersectionObserver` over *other people's* posts
  and calls the read API at `READ_THRESHOLD` visibility. DMs and posts in an
  open thread bypass the visibility check (`shouldForceReadPost`).

Both depend on Mattermost's internal DOM (`post_<id>`, `rhsPost_<id>`,
`.post__body`) and internal Redux shape (`state.views.rhs`, `state.views.threads`,
`postsInChannel`), none of which are public API. `utils/posts.ts` is where all
of that coupling lives — when a Mattermost upgrade breaks the ticks, look there
first. The selectors deliberately fall back to `undefined`/`[]` rather than
throwing, so an upgrade degrades to "no ticks" instead of a broken client.

`react`, `react-dom`, `redux` and `react-redux` are webpack **externals**: the
bundle uses the host app's copies. Don't add a runtime dependency that expects
its own React.

## Security model

The receipt list says who read a message and is treated as private to the
author. When touching the API, preserve these:

- `GET /api/v1/status` returns an entry **only if `status.AuthorID` equals the
  requesting user**. Without that check any account could dump the reader list
  for any post ID.
- `POST /api/v1/read` verifies `HasPermissionToChannel(reader, channel,
  PermissionReadChannel)` before recording anything, so receipts cannot be
  forged for channels the caller never joined.
- `markRead` returns `(nil, nil)` for unknown, invisible and own posts alike, and
  the handler answers `{"status":"ignored"}` for all three, so the response
  cannot be used to probe which post IDs exist.
- Handlers never return a raw `AppError` to the client — log it and send a
  generic message; `AppError` carries storage detail.
- `publishStatusUpdate` broadcasts with `WebsocketBroadcast{UserId: AuthorID}`.
  Never widen this to a channel broadcast.
- `syncDirectMessageRead` runs only *after* the `AuthorID == requester` check in
  `handleGetStatuses`, so a caller can never use it to probe someone else's
  channel view times. Keep it on that side of the check.
- Post IDs from clients go through `model.IsValidId` before reaching a KV key.
- Bounds that exist to stop unbounded growth: `maxReadBy` (per-post readers),
  `maxStatusPostIDs` (KV reads per request), `maxReadBodyBytes`,
  `statusTTLSeconds` (KV entries expire).

Hook and HTTP entry points `defer p.recoverPanic(...)`. `MessageHasBeenPosted`
sits on the message-posting path, so a panic there would take the plugin process
down with it.

## Styling

Tick colour comes from `rgba(var(--center-channel-color-rgb), <alpha>)` in
`styles/message_status.scss`; the SVG paths use `stroke='currentColor'`. This
keeps the ticks legible in light, dark and custom themes. **Do not reintroduce
hard-coded hex colours** (the pre-1.1 build used `#22C55E` green for read, which
breaks on dark themes and is not the intended Telegram look). Delivered and read
share one hue and differ only in alpha — the meaning is carried by one tick vs.
two.

The glyph itself is stroke-based, not a filled path. The coordinates in
`MessageStatusTicks.tsx` are the stroke centrelines of Material's `done_all`
outline, traced from its 24-unit grid — so the read state is two *overlapping*
checks with the rear one interrupted where the front crosses it, not two checks
placed side by side. Both states share a baseline and crop the same 15-unit
band of the viewBox, which is what keeps them from jumping when a post flips
from delivered to read; change one viewBox and you have to change the other.

The tick container is `position: absolute` inside a `position: relative` post
body so ticks never change post height. Changing that will shift the layout of
every message in the app.
