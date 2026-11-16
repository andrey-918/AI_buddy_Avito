"""Validate the current evaluation set and score a retrieval method.

Only the development split is scored here. The holdout split remains untouched
until retrieval approaches and thresholds are frozen for comparison.
"""

from __future__ import annotations

import json
from collections import Counter
from dataclasses import dataclass
from datetime import date
from pathlib import Path
from typing import Callable

from lexical_retrieval import SearchHit, offline_demo_context, search


ROOT = Path(__file__).resolve().parents[1]
KNOWLEDGE_PATH = ROOT / "data" / "knowledge.json"
EVAL_PATH = ROOT / "data" / "eval" / "questions-v2.json"
Ranker = Callable[[str, list[dict], int], list[SearchHit]]


@dataclass(frozen=True)
class RetrievalMetrics:
    questions: int
    hit_at_1: float
    recall_at_3: float
    mrr_at_3: float


def load_and_validate() -> dict:
    """Check that labels refer to this knowledge snapshot and allowed cards."""
    dataset = json.loads(EVAL_PATH.read_text(encoding="utf-8"))
    knowledge = json.loads(KNOWLEDGE_PATH.read_text(encoding="utf-8"))
    if dataset.get("schema_version") != "0.2":
        raise ValueError("unsupported evaluation schema")
    if dataset.get("knowledge_snapshot") != knowledge.get("snapshot_id"):
        raise ValueError("evaluation and knowledge snapshots differ")
    if dataset.get("context", {}).get("demo") is not True:
        raise ValueError("this evaluation requires explicit demo mode")

    as_of = date.fromisoformat(dataset["evaluation_time"][:10])
    cards = {card["id"]: card for card in knowledge["records"]}
    if len(cards) != len(knowledge["records"]):
        raise ValueError("duplicate knowledge card ID")
    seen_questions = set()
    for case in dataset["cases"]:
        if case["id"] in seen_questions:
            raise ValueError(f'duplicate question ID: {case["id"]}')
        seen_questions.add(case["id"])
        if case["split"] not in ("development", "holdout"):
            raise ValueError(f'invalid split: {case["id"]}')
        if case["role"] not in ("intern", "mentor", "manager"):
            raise ValueError(f'invalid role: {case["id"]}')

        expected = case["expected"]
        if expected["intent"] not in ("onboarding", "navigation", "values", "other"):
            raise ValueError(f'invalid intent: {case["id"]}')
        outcome = expected["outcome"]
        source_ids = expected["source_ids"]
        if outcome not in ("answer", "human_gate", "fallback"):
            raise ValueError(f'invalid outcome: {case["id"]}')
        if (outcome == "answer") != bool(source_ids):
            raise ValueError(f'answer/source mismatch: {case["id"]}')

        for source_id in source_ids:
            card = cards.get(source_id)
            if card is None:
                raise ValueError(f'unknown source {source_id}: {case["id"]}')
            if (card["status"] != "approved"
                    or date.fromisoformat(card["expires_at"]) < as_of
                    or case["role"] not in card["allowed_roles"]
                    or card["team_scope"] not in (dataset["context"]["team"], "all-demo-teams")):
                raise ValueError(f'ineligible source {source_id}: {case["id"]}')
            if card["approval_scope"] != "demo_only":
                raise ValueError(f'unexpected approval scope {source_id}: {case["id"]}')

        related = expected.get("related_card_id")
        if related is not None and related not in cards:
            raise ValueError(f'unknown related card {related}: {case["id"]}')
    return dataset


def evaluate(ranker: Ranker, dataset: dict, *, top_k: int = 3) -> RetrievalMetrics:
    """Score answerable development questions; any listed source counts as valid."""
    if top_k != 3:
        raise ValueError("this dataset defines Recall@3 and MRR@3")
    as_of = date.fromisoformat(dataset["evaluation_time"][:10])
    team = dataset["context"]["team"]
    totals = Counter()

    for case in dataset["cases"]:
        if case["split"] != "development" or case["expected"]["outcome"] != "answer":
            continue
        eligible = offline_demo_context(case["role"], team, as_of)
        hits = ranker(case["question"], eligible, top_k)
        acceptable = set(case["expected"]["source_ids"])
        rank = next((index for index, hit in enumerate(hits, 1) if hit.card_id in acceptable), None)
        totals["questions"] += 1
        totals["hit_at_1"] += rank == 1
        totals["recall_at_3"] += rank is not None
        totals["mrr_at_3"] += 1 / rank if rank is not None else 0
        print(f'{case["id"]}: rank={rank or "miss"}, candidates={[hit.card_id for hit in hits]}')

    count = totals["questions"]
    if count == 0:
        raise ValueError("no answerable development questions")
    return RetrievalMetrics(
        questions=count,
        hit_at_1=totals["hit_at_1"] / count,
        recall_at_3=totals["recall_at_3"] / count,
        mrr_at_3=totals["mrr_at_3"] / count,
    )


if __name__ == "__main__":
    metrics = evaluate(search, load_and_validate())
    print(f"BM25 development questions: {metrics.questions}")
    print(f"Hit@1: {metrics.hit_at_1:.3f}")
    print(f"Recall@3: {metrics.recall_at_3:.3f}")
    print(f"MRR@3: {metrics.mrr_at_3:.3f}")
