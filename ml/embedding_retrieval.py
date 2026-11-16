"""Local semantic retrieval over eligible knowledge cards.

Run with: uv run --python 3.12 --with fastembed==0.9.1 python ml/embedding_retrieval.py
The first run downloads the multilingual model to BUDDY_MODEL_CACHE (or the
user cache). No question or card text is sent to an inference API.
"""

from __future__ import annotations

import hashlib
import os
from dataclasses import dataclass

import numpy as np

from lexical_retrieval import SearchHit


MODEL_NAME = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"


@dataclass(frozen=True)
class CardVector:
    fingerprint: str
    vector: np.ndarray


class EmbeddingRetriever:
    """Embed approved context after access filtering, then compare by cosine."""

    def __init__(self, model_name: str = MODEL_NAME) -> None:
        try:
            from fastembed import TextEmbedding
        except ImportError as exc:
            raise RuntimeError("Install fastembed before running semantic retrieval") from exc

        cache_dir = os.getenv("BUDDY_MODEL_CACHE")
        self.model = TextEmbedding(model_name=model_name, cache_dir=cache_dir)
        self.card_vectors: dict[str, CardVector] = {}

    @staticmethod
    def _card_text(card: dict) -> str:
        # Match the same text fields used by the lexical baseline.
        return f'{card["title"]}. {card["evidence_text"]} {card["answer"]}'

    @staticmethod
    def _unit(vector: np.ndarray) -> np.ndarray:
        vector = np.asarray(vector, dtype=np.float32)
        length = float(np.linalg.norm(vector))
        if vector.ndim != 1 or not np.isfinite(length) or length == 0:
            raise ValueError("embedding model returned an invalid vector")
        return vector / length

    def search(self, question: str, eligible_cards: list[dict], top_k: int = 3) -> list[SearchHit]:
        """Return candidates only; similarity never authorizes a factual answer.

        The caller must run the local privacy guardrail before this method.
        Question text and its embedding are not cached or logged here.
        """
        if top_k < 1 or not eligible_cards or not question.strip():
            return []

        missing = []
        for card in eligible_cards:
            content = self._card_text(card)
            fingerprint = hashlib.sha256(content.encode("utf-8")).hexdigest()
            cached = self.card_vectors.get(card["id"])
            if cached is None or cached.fingerprint != fingerprint:
                missing.append((card["id"], fingerprint, content))
        if missing:
            vectors = list(self.model.embed([item[2] for item in missing]))
            if len(vectors) != len(missing):
                raise ValueError("embedding model returned the wrong number of card vectors")
            for (card_id, fingerprint, _), vector in zip(missing, vectors, strict=True):
                self.card_vectors[card_id] = CardVector(fingerprint, self._unit(vector))

        query_vectors = list(self.model.embed([question]))
        if len(query_vectors) != 1:
            raise ValueError("embedding model did not return one question vector")
        query_vector = self._unit(query_vectors[0])

        hits = []
        for card in eligible_cards:
            card_vector = self.card_vectors[card["id"]].vector
            if card_vector.shape != query_vector.shape:
                raise ValueError("card and question embedding dimensions differ")
            similarity = float(np.dot(query_vector, card_vector))
            if not np.isfinite(similarity):
                raise ValueError("embedding similarity is not finite")
            hits.append(SearchHit(card["id"], round(similarity, 4)))
        return sorted(hits, key=lambda hit: (-hit.score, hit.card_id))[:top_k]


def main() -> None:
    from evaluate_retrieval import evaluate, load_and_validate

    retriever = EmbeddingRetriever()
    metrics = evaluate(retriever.search, load_and_validate())
    print(f"Model: {MODEL_NAME}")
    print(f"Development questions: {metrics.questions}")
    print(f"Hit@1: {metrics.hit_at_1:.3f}")
    print(f"Recall@3: {metrics.recall_at_3:.3f}")
    print(f"MRR@3: {metrics.mrr_at_3:.3f}")


if __name__ == "__main__":
    main()
