"""Her senses: a small local server that turns a face or a voice into a
print, a list of numbers that is close for the same person and far for
different people.

Started by the Cosmos core, which is its only caller. It listens on this
computer only. Nothing it is given is kept: the core decides what to keep.
"""
import base64
import io
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

import numpy as np
import onnxruntime as ort
import soundfile as sf

port, face_model, voice_model = int(sys.argv[1]), sys.argv[2], sys.argv[3]

faces = ort.InferenceSession(face_model) if face_model else None
voices = None
if voice_model:
    import sherpa_onnx

    voices = sherpa_onnx.SpeakerEmbeddingExtractor(
        sherpa_onnx.SpeakerEmbeddingExtractorConfig(model=voice_model, num_threads=2)
    )


def unit(v):
    v = np.asarray(v, dtype=np.float32)
    n = float(np.linalg.norm(v))
    return (v / n if n > 0 else v).tolist()


def faceprint(rgb):
    """rgb: 112 x 112 x 3 bytes, the face already turned and scaled so the
    eyes, nose and mouth sit where the model expects them."""
    a = np.frombuffer(rgb, dtype=np.uint8)
    if a.size != 112 * 112 * 3:
        raise ValueError("a face must be 112 by 112 pixels")
    a = a.reshape(112, 112, 3).astype(np.float32)
    a = (a - 127.5) / 127.5
    a = a.transpose(2, 0, 1)[None]
    out = faces.run(None, {faces.get_inputs()[0].name: a})[0][0]
    return unit(out)


def voiceprint(wav):
    samples, rate = sf.read(io.BytesIO(wav), dtype="float32")
    if samples.ndim > 1:
        samples = samples.mean(axis=1)
    if len(samples) < rate * 0.5:
        raise ValueError("too little speech for a voiceprint")
    s = voices.create_stream()
    s.accept_waveform(rate, samples)
    s.input_finished()
    return unit(voices.compute(s))


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
        self.reply(200, {"ok": True, "faces": faces is not None, "voices": voices is not None})

    def do_POST(self):
        try:
            ask = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            data = base64.b64decode(ask["data"])
            if self.path == "/face" and faces is not None:
                self.reply(200, {"print": faceprint(data)})
            elif self.path == "/voice" and voices is not None:
                self.reply(200, {"print": voiceprint(data)})
            else:
                self.reply(404, {"error": "that sense is not set up"})
        except Exception as err:  # the core shows this to the user
            self.reply(500, {"error": str(err)})


HTTPServer(("127.0.0.1", port), Handler).serve_forever()
