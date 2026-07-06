"""
Run this ONCE while you have internet to pre-download the embedding model.
After this, everything runs offline.
"""
from sentence_transformers import SentenceTransformer

print("Downloading all-MiniLM-L6-v2 embedding model...")
model = SentenceTransformer("all-MiniLM-L6-v2")
print("Done. Test embedding...")
vec = model.encode("hello cosmos", normalize_embeddings=True)
print(f"Embedding shape: {vec.shape} — all good.")
