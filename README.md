# Cosmos

Cosmos is the home of Nova, a companion that runs on your own machine. It
replaces the archived `nova-main` app.

It has two parts:

- `core/` is her mind: one Go program (`cosmos-core`) that owns her memory,
  mood, values, intentions and sleep, and talks to local models through
  llama.cpp's OpenAI-compatible API.
- `app/` is the window: an Electron app that starts the core and shows what
  she says and what is going on inside her.

## Run it

You need Go, Node and a `llama-server` binary with at least one GGUF model.

```bash
cd app
npm install
npm run dev      # builds the core, then opens the app with live reload
npm start        # same, from a production build
```

On first start open **Models**. It lists the `.gguf` files in
`~/models` and in `models/` inside her data folder (add more folders with
`COSMOS_MODELS`). Pick a file for the main model and press Start. Each
model has Start, Stop and Restart, and a log of what `llama-server` printed.
The worker and sleep models use the main model until you pick their own.
If you run `llama-server` yourself, just enter its address instead.

Her data lives in `~/Library/Application Support/Cosmos` (`cosmos.db` and
`config.json`). Set `COSMOS_DATA` to use another folder.

To keep her awake with the window closed, or to run her on another machine,
start the core on its own and point the app at it:

```bash
COSMOS_TOKEN=choose-a-secret core/bin/cosmos-core --addr 127.0.0.1:7717
COSMOS_CORE_URL=http://127.0.0.1:7717 COSMOS_TOKEN=choose-a-secret npm start
```

## How she works

**Awake.** Each message goes through one loop, run by plain code:

1. **route** (main model): picks the tools needed and a planning mode.
2. **tools** (code): weather, calendar, memory and people lookups.
3. **notice** (main model): how the moment lands on her, judged against her
   values and the people close to you. This moves her mood and may queue a
   follow-up.
4. **writer** (main model): writes the reply in her voice from the facts.
5. **verify**: code checks every number against the facts; the main model
   checks replies that used tools. A failed check earns one rewrite.

Everything is written to her memory stream as it happens. After the reply,
a capture step keeps her long-term facts about you up to date.

**Sessions.** A session is one sitting. Her memories, people, mood and
intentions carry across all of them; only the thread starts clean. A new
one starts when you pick "New session", after two hours of silence, or
when she wakes. The list under her name shows only the sessions she has
not slept on. Once she sleeps, they move to the Sleep screen with what she
kept from each.

**Initiative.** She speaks first only when she has a reason. Conversations,
sleep and the weather watch put intentions in a queue with a trigger time.
A heartbeat checks the queue every 30 seconds; no model runs while idle. A
gate applies quiet hours, a cooldown and a daily budget. Reminders you asked
for always go out.

**Sleep.** At night (or from the Sleep screen) the sleep model reads the
day's stream and writes a journal entry and reflections, tidies duplicate
memories (each change can be undone), and may propose changes to her
values, her writing or her planning. Proposals wait for your approval.

**Guards.** Small models make things up, so code checks their claims
against the actual text: memories must come from words you said, people
must have been named, nothing is removed from the calendar unless you asked,
and replies that copy the prompt or loop are cut.

## Tests

```bash
cd core && go test ./...
```

The brain's tests use a scripted model and a fake clock, so they need no
model server.

## Bringing data over from Nova

```bash
core/bin/cosmos-core --import-nova "<Nova's data folder>"
```

This reads `diary.json` and `humans.json` and adds memories, journal entries
and people that are not already present. It only reads from the Nova
folder. Calendar entries are not imported.

## Not built yet

- Discord and images from Nova.
- Memory search is keyword based; there are no embeddings.
- Image and audio specialist models.
- Replies appear whole, not streamed token by token.
- Packaging the app for distribution.
