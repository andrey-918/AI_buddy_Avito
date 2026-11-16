"""Compare BM25, local embeddings, and rank fusion on development questions.

Run with: uv run --python 3.12 --with fastembed==0.9.1 python ml/compare_retrieval.py
The holdout split is intentionally not scored while approaches are being chosen.
"""

from __future__ import annotations

import json
from collections import defaultdict
from datetime import date
from pathlib import Path

from embedding_retrieval import EmbeddingRetriever
from evaluate_retrieval import load_and_validate
from lexical_retrieval import SearchHit, offline_demo_context, search as lexical_search


ROOT = Path(__file__).resolve().parents[1]
FUSION_K = 60


def reciprocal_rank_fusion(*rankings: list[SearchHit], top_k: int = 3) -> list[SearchHit]:
    """Combine positions, not incompatible BM25 and cosine score scales."""
    fused: dict[str, float] = defaultdict(float)
    for ranking in rankings:
        for position, hit in enumerate(ranking, start=1):
            fused[hit.card_id] += 1 / (FUSION_K + position)
    ordered = sorted(fused.items(), key=lambda item: (-item[1], item[0]))
    return [SearchHit(card_id, score) for card_id, score in ordered[:top_k]]


def rank_of_accepted(hits: list[SearchHit], acceptable: set[str]) -> int | None:
    return next((position for position, hit in enumerate(hits, 1) if hit.card_id in acceptable), None)


def report() -> None:
    dataset = load_and_validate()
    as_of = date.fromisoformat(dataset["evaluation_time"][:10])
    team = dataset["context"]["team"]
    dense = EmbeddingRetriever()
    ranks: dict[str, list[int | None]] = {name: [] for name in ("BM25", "embeddings", "hybrid")}

    for case in dataset["cases"]:
        if case["split"] != "development" or case["expected"]["outcome"] != "answer":
            continue
        eligible = offline_demo_context(case["role"], team, as_of)
        lexical = lexical_search(case["question"], eligible, top_k=len(eligible))
        semantic = dense.search(case["question"], eligible, top_k=len(eligible))
        hybrid = reciprocal_rank_fusion(lexical, semantic, top_k=len(eligible))
        acceptable = set(case["expected"]["source_ids"])
        for name, hits in (("BM25", lexical), ("embeddings", semantic), ("hybrid", hybrid)):
            ranks[name].append(rank_of_accepted(hits, acceptable))

        positions = tuple(ranks[name][-1] for name in ranks)
        if len(set(positions)) > 1:
            print(f'{case["id"]}: BM25={positions[0]}, embeddings={positions[1]}, hybrid={positions[2]}')

    print(f"Development answer questions: {len(ranks['BM25'])}")
    for name, positions in ranks.items():
        count = len(positions)
        hit_1 = sum(rank == 1 for rank in positions) / count
        recall_3 = sum(rank is not None and rank <= 3 for rank in positions) / count
        mrr_3 = sum(1 / rank for rank in positions if rank is not None and rank <= 3) / count
        print(f"{name}: Hit@1={hit_1:.3f}, Recall@3={recall_3:.3f}, MRR@3={mrr_3:.3f}")

    # Q002 is a known development paraphrase from the earlier four-card set.
    # It is diagnostic only and does not affect the v2 metrics or tuning.
    old_cases = json.loads((ROOT / "data" / "eval" / "questions.json").read_text(encoding="utf-8"))["cases"]
    diagnostic = next(case for case in old_cases if case["id"] == "Q002" and case["split"] == "development")
    eligible = offline_demo_context("intern", team, as_of)
    lexical = lexical_search(diagnostic["question"], eligible, top_k=len(eligible))
    semantic = dense.search(diagnostic["question"], eligible, top_k=len(eligible))
    hybrid = reciprocal_rank_fusion(lexical, semantic, top_k=len(eligible))
    print("Q002 diagnostic, expected K-101:")
    for name, hits in (("BM25", lexical), ("embeddings", semantic), ("hybrid", hybrid)):
        print(f"  {name}: rank={rank_of_accepted(hits, {'K-101'}) or 'miss'}; top3={[hit.card_id for hit in hits[:3]]}")


if __name__ == "__main__":
    report()
