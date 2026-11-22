# Her sense of when someone is speaking: Silero VAD, run on this computer.
# Started by the core for one call. It reads sound from standard input,
# 512 samples at a time (16 kHz mono, 16-bit), and writes back for each
# block how sure it is that it holds speech, as one 32-bit float. Nothing
# goes anywhere else.
#
#   python vad_server.py <silero_vad.onnx>

import struct
import sys

import numpy as np
import onnxruntime as ort

BLOCK = 512
CONTEXT = 64  # the end of the last block, which the model reads with the next

opts = ort.SessionOptions()
opts.intra_op_num_threads = 1
opts.inter_op_num_threads = 1
model = ort.InferenceSession(sys.argv[1], sess_options=opts, providers=["CPUExecutionProvider"])
state = np.zeros((2, 1, 128), dtype=np.float32)
context = np.zeros((1, CONTEXT), dtype=np.float32)
rate = np.array(16000, dtype=np.int64)

inp = sys.stdin.buffer
out = sys.stdout.buffer
# Loaded: a negative number says so.
out.write(struct.pack("<f", -1.0))
out.flush()

while True:
    raw = inp.read(BLOCK * 2)
    if len(raw) < BLOCK * 2:
        break
    block = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0
    x = np.concatenate([context, block[None, :]], axis=1)
    prob, state = model.run(None, {"input": x, "state": state, "sr": rate})
    context = x[:, -CONTEXT:]
    out.write(struct.pack("<f", float(prob[0][0])))
    out.flush()
