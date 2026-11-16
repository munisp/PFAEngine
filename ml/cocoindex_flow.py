"""CocoIndex flow: incremental indexing of member statements and knowledge
articles into the vector store for semantic member support (RAG).

Value to the platform: CocoIndex's incremental engine re-embeds only changed
documents, so the member-support assistant always retrieves from FRESH
statements/regulations (PenCom circulars, fund fact sheets) instead of a
stale index. Runs as a sidecar; the platform's answers degrade gracefully to
the deterministic chatbot when the index is unavailable.

Requires: pip install cocoindex + a running Postgres for state.
Run:  python ml/cocoindex_flow.py
"""
from __future__ import annotations

import os

try:
    import cocoindex
except ImportError:  # pragma: no cover — optional sidecar
    cocoindex = None

DOC_SOURCES = os.getenv("PFA_DOC_SOURCES", "docs/,knowledge/")
VECTOR_TABLE = os.getenv("PFA_VECTOR_TABLE", "member_support_embeddings")


def main() -> None:  # pragma: no cover — requires cocoindex server
    if cocoindex is None:
        raise SystemExit("pip install cocoindex to run the indexing flow")
    # Declarative flow: local docs -> chunk -> embed -> pgvector table.
    # CocoIndex tracks pipeline state; re-runs re-process only changed files.
    import cocoindex as coco

    @coco.main_fn()
    def flow():
        docs = coco.add_source("LocalFiles", root=list(DOC_SOURCES.split(",")))
        out = coco.add_target("Postgres", table=VECTOR_TABLE)
        with docs.row() as doc:
            for chunk in doc["content"].transform(
                    coco.functions.ChunkRecursively(chunk_size=900)):
                out.collect(
                    filename=doc["filename"],
                    text=chunk["text"],
                    embedding=chunk["text"].transform(
                        coco.functions.EmbedText(
                            "sentence-transformers/all-MiniLM-L6-v2")))
    flow.update()


if __name__ == "__main__":
    main()
