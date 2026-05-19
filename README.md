# Eibanban

Eibanban is a minimal native desktop chat app built with Go and Gio. It talks to an OpenAI-compatible Responses API endpoint, supports text chat, image attachments, image generation, local memories, and SQLite-backed conversation history.

## Features

- Native cross-platform GUI with Gio
- OpenAI-compatible Responses API client with streaming support
- Image generation through the `image_generation` tool
- Image attachment upload, including SVG conversion and transparent-image white background preparation
- Click-to-preview images with zoom controls and panning
- Persistent conversation history in SQLite
- Fixed context injection from `character.md` and `memories.md`
- Reads Codex-style config from `~/.codex/config.toml` and `~/.codex/auth.json`

## Project Layout

```text
cmd/eibanban/        Application entrypoint
internal/app/        Gio UI, API client, storage, image handling, and app logic
character.md         Character prompt injected into every request
memories.md          Memory document injected into every request
```

The code is kept under `internal/` because it is application-private for now. If a reusable public library emerges later, it can be promoted into `pkg/`.

## Run

```bash
go run ./cmd/eibanban
```

## Build

```bash
go build ./cmd/eibanban
```

## Configuration

By default, Eibanban reads the same style of configuration used by Codex:

```text
~/.codex/config.toml
~/.codex/auth.json
```

Expected auth shape:

```json
{
  "OPENAI_API_KEY": "your-api-key"
}
```

The app uses the configured `base_url` and `model`, and sends requests to the Responses API.

## Local Data

Conversation history is stored in SQLite:

```text
~/Library/Application Support/chengcheng-chat/chat.db
```

Generated and prepared images are written under:

```text
app_outputs/
```

`app_outputs/` is ignored by Git.

## Development

Run all checks:

```bash
go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
