"""Her voice: a small local server that turns text into speech with Kokoro.

Started by the Cosmos core, which is its only caller. It listens on this
computer only and keeps the model loaded between replies.

With the timing-aware model it also says when each speech sound starts and
how long it lasts, so her mouth can form the sounds as they are heard.
"""
import base64
import io
import json
import re
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

import numpy as np
import onnxruntime as ort
import soundfile as sf
from kokoro_onnx import Kokoro

port, model, voices = int(sys.argv[1]), sys.argv[2], sys.argv[3]
timed_path = sys.argv[4] if len(sys.argv) > 4 else ""
kokoro = Kokoro(model, voices)
known = set(kokoro.get_voices())
vocab = kokoro.tokenizer.vocab
timed = ort.InferenceSession(timed_path) if timed_path else None
RATE = 24000
# The model takes at most this many sounds at once.
LIMIT = 500


def pieces(text):
    """Split text into sentences, so each fits the model."""
    out, cur = [], ""
    for i, ch in enumerate(text):
        cur += ch
        if ch in ".!?\n" and (i + 1 == len(text) or text[i + 1] in " \n"):
            if cur.strip():
                out.append(cur.strip())
            cur = ""
    if cur.strip():
        out.append(cur.strip())
    return out


# The quiet the model puts before a sentence, kept down to this: the
# rest is only a wait before she is heard.
LEAD = 0.05


def speak_timed(text, voice, speed, lang):
    """Sound, and a list of [start, length, sound] for every speech sound."""
    style_all = kokoro.get_voice_style(voice)
    audio, marks, at = [], [], 0.0
    # Three dots make the timing model give nothing but quiet for the
    # whole sentence; the single character is read as the pause it is.
    text = re.sub(r"\.{2,}", "…", text)
    for piece in pieces(text):
        sounds = [c for c in kokoro.tokenizer.phonemize(piece, lang) if c in vocab][:LIMIT]
        if not sounds:
            continue
        ids = [vocab[c] for c in sounds]
        wave, dur = timed.run(None, {
            "input_ids": np.array([[0, *ids, 0]], dtype=np.int64),
            "style": style_all[len(ids)].astype(np.float32).reshape(1, 256),
            "speed": np.array([speed], dtype=np.float32),
        })
        wave, dur = wave[0], dur[0]
        total = float(dur.sum())
        # Seconds per unit of the model's own timing.
        unit = (len(wave) / RATE) / total if total > 0 else 0.025
        lead = float(dur[0]) * unit
        cut = max(0.0, lead - LEAD)
        wave = wave[int(cut * RATE):]
        t = at + lead - cut
        for sound, d in zip(sounds, dur[1:-1]):
            length = float(d) * unit
            marks.append([round(t, 3), round(length, 3), sound])
            t += length
        audio.append(wave)
        at += len(wave) / RATE
    if not audio:
        raise ValueError("nothing in that could be spoken")
    return np.concatenate(audio), marks


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.reply(200, {"ok": True, "timed": timed is not None})

    def do_POST(self):
        try:
            ask = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            voice = ask.get("voice") or "af_heart"
            if voice not in known:
                voice = "af_heart"
            speed = float(ask.get("speed") or 1.0)
            # Voices whose name starts with b are British.
            lang = "en-gb" if voice.startswith("b") else "en-us"
            if timed is not None:
                samples, marks = speak_timed(ask["text"], voice, speed, lang)
            else:
                samples, _ = kokoro.create(ask["text"], voice=voice, speed=speed, lang=lang)
                marks = []
            out = io.BytesIO()
            sf.write(out, samples, RATE, format="WAV", subtype="PCM_16")
            self.reply(200, {"audio": base64.b64encode(out.getvalue()).decode(), "marks": marks})
        except Exception as err:  # the core shows this to the user
            self.reply(500, {"error": str(err)})


HTTPServer(("127.0.0.1", port), Handler).serve_forever()
