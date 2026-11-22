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
3. **writer** (main model): writes the reply in her voice from the facts.
4. **verify**: code checks every number against the facts; the main model
   checks replies that used tools. A failed check earns one rewrite.

Everything is written to her memory stream as it happens. After the reply,
in the background: **notice** (worker) judges how the moment landed on her
against her values and the people close to you, which moves her mood and
may queue a follow-up; a capture step keeps her long-term facts about you
up to date. Each background job takes the model on its own and lets a new
message go first.

**Names.** Her name, your name and the platform's name are settings
(`companion_name`, `user_name`, `platform_name`), never written into code
or prompts.

**Talking to her.** The microphone button opens her ears. By default she
listens for her name: she answers when she hears it, or anything that
sounds like it ("Also answers to" in Settings adds spellings), and for a
short while after each reply without it. Talk not meant for her is kept
for a few minutes in memory only, so a reply she is asked for knows what
was just said; it is never written to her stream, memories or sleep.
"Answer everything" turns the name gate off.

Speech is written out by `whisper-server` (from `brew install
whisper-cpp`), kept running while she listens and stopped after 15 idle
minutes; the model is the first `.bin` in `~/models/whisper` unless
`hear_model` names one. Her voice is Kokoro, started when listening
starts. Cutting in stops her only once it is clearly someone speaking
(and, listening for her name, someone she is talking with); words said
while she is working are never dropped, and cut her current reply short
so she answers everything together.

**Faces and voices.** With her camera on, a face she follows for a moment
is lined up in the camera worker and turned into a faceprint; a crop is
kept in `sight/` in her data folder. A face she knows is a sighting of that
person (once per ten minutes). One she does not know joins others like it,
and after she sleeps she asks "Who is this?" in the chat with the picture:
name them, pick someone she knows, say it is you, or have her forget them,
which deletes the pictures. Strangers' pictures are let go after
`unknown_face_days`. The People page shows each person's pictures and the
faces still to answer. Teach her your voice and her own in Settings → Her
ears. She then ignores her own voice from the speakers and, listening for
her name, answers only you (unless "Answer others" is on). A voice she does
not know, heard while one person she knows is in view, becomes theirs.
Camera, pictures and prints never leave this computer.

Two model files, downloaded once, plus `sherpa-onnx` in her voice's
environment:

```bash
mkdir -p ~/models/sight ~/models/speaker
curl -L -o ~/models/sight/w600k_mbf.onnx https://huggingface.co/deepghs/insightface/resolve/main/buffalo_s/w600k_mbf.onnx
curl -L -o ~/models/speaker/wespeaker_en_voxceleb_resnet34_LM.onnx https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/wespeaker_en_voxceleb_resnet34_LM.onnx
uv pip install --python "$HOME/Library/Application Support/Cosmos/voice/.venv/bin/python" sherpa-onnx
```

For her to describe what she sees when you ask her to look, pick the main
model's "Vision projector" in Models and restart it.

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
