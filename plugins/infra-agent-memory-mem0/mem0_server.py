"""Mem0 REST API server with embedded Qdrant vector store.

Reads configuration from environment variables set by the Go sidecar
(which fetches them from the kernel plugin config).
"""

import os
import json
import logging
import threading
from contextlib import asynccontextmanager

import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from mem0 import Memory

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("mem0-server")

ENV_FILE = "/data/mem0.env"
QDRANT_PATH = "/data/qdrant"


def load_env_file():
    """Load env vars from file written by Go sidecar (bridges kernel config to Python).
    Waits up to 30s for the sidecar to write the file.
    """
    import pathlib
    import time as _time
    p = pathlib.Path(ENV_FILE)
    for _ in range(15):
        if p.exists():
            break
        logger.info("Waiting for %s from sidecar...", ENV_FILE)
        _time.sleep(2)
    if not p.exists():
        logger.warning("No %s found after waiting, using OS environment only", ENV_FILE)
        return
    for line in p.read_text().strip().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, _, value = line.partition("=")
        if key and value:
            os.environ.setdefault(key, value)
    logger.info("Loaded config from %s", ENV_FILE)


def _detect_existing_dims() -> int:
    """Check for an existing memories_NNN collection on disk and return its dimensions.
    Returns 0 if no collection exists (first run)."""
    import pathlib
    qdrant_path = pathlib.Path(QDRANT_PATH) / "collection"
    if not qdrant_path.is_dir():
        return 0
    for entry in qdrant_path.iterdir():
        if entry.is_dir() and entry.name.startswith("memories_"):
            try:
                dims = int(entry.name.split("_", 1)[1])
                if dims > 0:
                    logger.info("Reusing existing collection dimensions: %d (from %s)", dims, entry.name)
                    return dims
            except ValueError:
                continue
    return 0


def _detect_embedding_dims(embedder_config: dict, provider: str, base_url: str) -> int:
    """Probe the embedding endpoint to discover vector dimensions.

    Retries indefinitely until the endpoint responds — the plugin cannot
    start without knowing the correct dimensions (wrong dims = wrong
    collection = data loss).
    """
    import urllib.request
    import time as _time

    url = base_url.rstrip("/") + "/embeddings"
    if provider == "ollama":
        url = base_url.rstrip("/") + "/api/embed"

    payload = json.dumps({"model": embedder_config["model"], "input": "dimension probe"}).encode()
    attempt = 0

    while True:
        attempt += 1
        try:
            req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
            if embedder_config.get("api_key"):
                req.add_header("Authorization", f"Bearer {embedder_config['api_key']}")

            resp = urllib.request.urlopen(req, timeout=30)
            result = json.loads(resp.read())

            if provider == "ollama":
                dims = len(result.get("embeddings", [[]])[0])
            else:
                dims = len(result.get("data", [{}])[0].get("embedding", []))

            if dims > 0:
                logger.info("Auto-detected embedding dimensions: %d (model=%s)", dims, embedder_config["model"])
                return dims

            logger.warning("Embedding probe returned 0 dimensions, retrying...")
        except Exception as e:
            logger.warning("Embedding probe attempt %d failed: %s — retrying in 5s", attempt, e)

        _time.sleep(5)


# ── Configuration ────────────────────────────────────────────────────────────────

def build_config() -> dict:
    """Build Mem0 config from environment variables.

    The Go sidecar resolves plugin selectors into concrete values:
    - Provider is always "openai" (all agent plugins expose OpenAI-compat endpoints)
    - Base URL points to the kernel route proxy for the selected plugin
    - API key is "not-needed" (agent plugins handle their own auth)
    """
    llm_provider = os.environ.get("MEM0_LLM_PROVIDER", "openai")
    llm_model = os.environ.get("MEM0_LLM_MODEL", "llama3.2:3b")
    llm_api_key = os.environ.get("MEM0_LLM_API_KEY", "not-needed")
    llm_base_url = os.environ.get("MEM0_LLM_BASE_URL", "")

    embedder_provider = os.environ.get("MEM0_EMBEDDER_PROVIDER", "openai")
    embedder_model = os.environ.get("MEM0_EMBEDDER_MODEL", "nomic-embed-text")
    embedder_api_key = os.environ.get("MEM0_EMBEDDER_API_KEY", llm_api_key)
    embedder_base_url = os.environ.get("MEM0_EMBEDDER_BASE_URL", llm_base_url)

    # Build LLM config — use the appropriate key for the provider.
    llm_config = {"model": llm_model}
    if llm_api_key:
        llm_config["api_key"] = llm_api_key
    if llm_base_url:
        if llm_provider == "ollama":
            llm_config["ollama_base_url"] = llm_base_url
        else:
            llm_config["openai_base_url"] = llm_base_url

    # Build embedder config.
    embedder_config = {"model": embedder_model}
    if embedder_api_key:
        embedder_config["api_key"] = embedder_api_key
    if embedder_base_url:
        if embedder_provider == "ollama":
            embedder_config["ollama_base_url"] = embedder_base_url
        else:
            embedder_config["openai_base_url"] = embedder_base_url

    # Try to reuse dimensions from an existing collection on disk before probing.
    # This avoids blocking startup when the embedder is unavailable.
    embed_dims = _detect_existing_dims()
    if embed_dims == 0:
        # No existing collection — must probe to discover dimensions.
        embed_dims = _detect_embedding_dims(embedder_config, embedder_provider, embedder_base_url)

    # Tell Mem0's embedder the correct dimensions so it passes them
    # to the OpenAI SDK (which forwards as `dimensions` parameter).
    embedder_config["embedding_dims"] = embed_dims

    # Dimension-keyed collection name — allows switching embedding models
    # without losing data. Old collections stay as-is and get synced in background.
    collection_name = f"memories_{embed_dims}"

    return {
        "llm": {
            "provider": llm_provider,
            "config": llm_config,
        },
        "embedder": {
            "provider": embedder_provider,
            "config": embedder_config,
        },
        "vector_store": {
            "provider": "qdrant",
            "config": {
                "collection_name": collection_name,
                "path": QDRANT_PATH,
                "embedding_model_dims": embed_dims,
                "on_disk": True,
            },
        },
        "custom_fact_extraction_prompt": (
            "Extract key facts from the conversation. Return valid JSON with a "
            '"facts" key containing a list of plain strings.\n'
            'Example: {"facts": ["user prefers dark mode", "user is a software engineer"]}\n'
            'If no facts: {"facts": []}\n'
            "IMPORTANT: Each fact MUST be a plain string, NOT an object. "
            "Do NOT use keys like content, category, or tags.\n"
            "Return ONLY the JSON object, no markdown or code blocks."
        ),
        "version": "v1.1",
    }


# ── Migration: background sync between collections ──────────────────────────────

_migration_status = {"active": False, "processed": 0, "total": 0}


def _find_old_collections(qc, current_collection: str) -> list[str]:
    """Find any memories_* collections that aren't the current one.
    Uses the provided QdrantClient (from memory_client.vector_store.client)
    to avoid opening a second connection to the embedded Qdrant DB.
    """
    try:
        old = []
        for c in qc.get_collections().collections:
            name = c.name
            # Match "memories" (legacy) or "memories_NNN" (dimension-keyed)
            if name == "memories" or (name.startswith("memories_") and name != current_collection):
                info = qc.get_collection(name)
                if (info.points_count or 0) > 0:
                    old.append(name)
        return old
    except Exception as e:
        logger.warning("Failed to scan for old collections: %s", e)
        return []


def _sync_collections(qc, old_collection: str, new_collection: str,
                      embedder_url: str, embedder_model: str, embedder_api_key: str):
    """Background sync: stream text from old collection, re-embed, upsert into new.

    Reads payloads from old collection (text stored under "data" key),
    generates new embeddings via the embedding endpoint, and inserts into
    the new collection with the same point IDs and metadata.
    Reuses the QdrantClient from memory_client.vector_store.client.
    Does NOT use memory_client.add() — that would re-run LLM fact extraction.
    """
    global _migration_status
    from qdrant_client.models import PointStruct

    logger.info("Starting background sync: %s → %s", old_collection, new_collection)

    try:
        info = qc.get_collection(old_collection)
        total = info.points_count or 0
        _migration_status = {"active": True, "processed": 0, "total": total}

        offset = None
        processed = 0
        batch_size = 20

        while True:
            results, next_offset = qc.scroll(
                old_collection, limit=batch_size, offset=offset,
                with_payload=True, with_vectors=False,
            )
            if not results:
                break

            # Extract texts and metadata from payloads.
            points_data = []
            for point in results:
                payload = point.payload or {}
                text = payload.get("data", "")
                if not text:
                    processed += 1
                    continue
                points_data.append((point.id, text, payload))

            if points_data:
                # Batch embed all texts.
                texts = [pd[1] for pd in points_data]
                embeddings = _batch_embed(texts, embedder_url, embedder_model, embedder_api_key)

                if embeddings and len(embeddings) == len(points_data):
                    # Upsert into new collection.
                    points = []
                    for (pid, _text, payload), vector in zip(points_data, embeddings):
                        points.append(PointStruct(id=pid, vector=vector, payload=payload))
                    qc.upsert(collection_name=new_collection, points=points)

                    processed += len(points_data)
                    _migration_status["processed"] = processed
                    logger.info("Sync progress: %d/%d", processed, total)
                else:
                    logger.warning("Embedding batch failed, skipping %d points", len(points_data))
                    processed += len(points_data)
            else:
                processed += len(results)

            _migration_status["processed"] = processed

            if next_offset is None:
                break
            offset = next_offset

        logger.info("Background sync complete: %d points from %s → %s", processed, old_collection, new_collection)
    except Exception as e:
        logger.error("Background sync failed: %s", e)
    finally:
        _migration_status = {"active": False, "processed": 0, "total": 0}


def _batch_embed(texts: list[str], embedder_url: str, model: str, api_key: str) -> list[list[float]]:
    """Call the embedding endpoint for a batch of texts."""
    import urllib.request
    try:
        url = embedder_url.rstrip("/") + "/embeddings"
        payload = json.dumps({"model": model, "input": texts}).encode()
        req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
        if api_key:
            req.add_header("Authorization", f"Bearer {api_key}")
        resp = urllib.request.urlopen(req, timeout=120)
        result = json.loads(resp.read())
        return [d["embedding"] for d in result.get("data", [])]
    except Exception as e:
        logger.warning("Batch embed failed: %s", e)
        return []


# ── App lifecycle ────────────────────────────────────────────────────────────────

memory_client: Memory = None  # type: ignore
_current_config_hash: str = ""


def _config_hash() -> str:
    """Return a hash of the current env file to detect changes."""
    import hashlib, pathlib
    p = pathlib.Path(ENV_FILE)
    if not p.exists():
        return ""
    return hashlib.md5(p.read_bytes()).hexdigest()


def _init_memory():
    """(Re)initialize the Mem0 Memory client from current environment."""
    global memory_client, _current_config_hash
    load_env_file()
    cfg = build_config()
    logger.info("Initializing Mem0 with config: %s", json.dumps({
        k: {**v, "config": {kk: "***" if "key" in kk else vv for kk, vv in v.get("config", {}).items()}}
        for k, v in cfg.items() if isinstance(v, dict)
    }))

    # Initialize Mem0 — creates the dimension-keyed collection if needed.
    # Service is available immediately, even if background sync is pending.
    memory_client = Memory.from_config(cfg)
    _current_config_hash = _config_hash()

    current_collection = cfg["vector_store"]["config"]["collection_name"]
    embed_dims = cfg["vector_store"]["config"]["embedding_model_dims"]
    logger.info("Mem0 initialized (collection=%s, dims=%d, hash=%s)",
                current_collection, embed_dims, _current_config_hash)

    # Check for old collections that need syncing into the current one.
    # Reuse Mem0's Qdrant client — embedded Qdrant doesn't allow concurrent access.
    qc = memory_client.vector_store.client
    old_collections = _find_old_collections(qc, current_collection)
    if old_collections:
        embedder_cfg = cfg["embedder"]["config"]
        embedder_url = embedder_cfg.get("openai_base_url") or embedder_cfg.get("ollama_base_url", "")
        embedder_model = embedder_cfg["model"]
        embedder_api_key = embedder_cfg.get("api_key", "")

        for old in old_collections:
            logger.info("Found old collection %s — starting background sync → %s", old, current_collection)
            t = threading.Thread(
                target=_sync_collections,
                args=(qc, old, current_collection, embedder_url, embedder_model, embedder_api_key),
                daemon=True,
            )
            t.start()


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Initialize Mem0 in a background thread so the server starts immediately.
    # Endpoints return 503 until memory_client is ready.
    t = threading.Thread(target=_init_memory, daemon=True)
    t.start()
    yield


app = FastAPI(title="Mem0 Memory Server", lifespan=lifespan)


def _require_memory():
    """Raise 503 if Mem0 is not yet initialized (embedder still probing)."""
    if memory_client is None:
        raise HTTPException(status_code=503, detail="Mem0 initializing — embedder not yet available")


# ── Health ───────────────────────────────────────────────────────────────────────

@app.get("/")
async def health():
    ready = memory_client is not None
    return {"status": "healthy" if ready else "initializing", "service": "mem0", "ready": ready}


# ── Hot reload ───────────────────────────────────────────────────────────────────

@app.post("/reload")
async def reload_config():
    """Reinitialize Mem0 with updated config from env file.
    Called by the Go sidecar when kernel config changes."""
    _require_memory()
    current = _config_hash()
    if current == _current_config_hash and current != "":
        return {"status": "no_change", "config_hash": current}
    _init_memory()
    return {"status": "reloaded", "config_hash": _current_config_hash}


# ── Migration status ─────────────────────────────────────────────────────────────

@app.get("/migration/status")
async def migration_status():
    return _migration_status


# ── Memories CRUD ────────────────────────────────────────────────────────────────

@app.post("/v1/memories/")
async def add_memories(request: Request):
    _require_memory()
    body = await request.json()
    messages = body.get("messages", [])
    if not messages:
        raise HTTPException(status_code=400, detail="messages is required")

    kwargs = {}
    for key in ("user_id", "agent_id", "app_id", "run_id", "metadata",
                "infer", "immutable", "enable_graph", "expiration_date",
                "custom_categories", "custom_instructions"):
        if key in body and body[key] is not None:
            kwargs[key] = body[key]

    result = memory_client.add(messages, **kwargs)
    return {"results": result.get("results", result) if isinstance(result, dict) else result}


@app.post("/v1/memories/search/")
async def search_memories(request: Request):
    _require_memory()
    body = await request.json()
    query = body.get("query", "")
    if not query:
        raise HTTPException(status_code=400, detail="query is required")

    kwargs = {}
    for key in ("user_id", "agent_id", "app_id", "run_id",
                "threshold", "rerank", "keyword_search"):
        if key in body and body[key] is not None:
            kwargs[key] = body[key]
    # Mem0 uses "limit" not "top_k" for max results.
    if body.get("top_k") is not None:
        kwargs["limit"] = body["top_k"]

    results = memory_client.search(query, **kwargs)
    return {"results": results.get("results", results) if isinstance(results, dict) else results}


@app.get("/v1/memories/count")
async def count_memories(
    user_id: str = None,
    agent_id: str = None,
    app_id: str = None,
    run_id: str = None,
):
    """Return total memory count. Uses a high limit to count all."""
    _require_memory()
    kwargs = {}
    if user_id:
        kwargs["user_id"] = user_id
    if agent_id:
        kwargs["agent_id"] = agent_id
    if app_id:
        kwargs["app_id"] = app_id
    if run_id:
        kwargs["run_id"] = run_id

    raw = memory_client.get_all(**kwargs, limit=100000)
    results = raw.get("results", raw) if isinstance(raw, dict) else raw
    total = len(results) if isinstance(results, list) else 0
    return {"total": total}


@app.get("/v1/memories/")
async def list_memories(
    user_id: str = None,
    agent_id: str = None,
    app_id: str = None,
    run_id: str = None,
    page: int = None,
    page_size: int = None,
):
    _require_memory()
    kwargs = {}
    if user_id:
        kwargs["user_id"] = user_id
    if agent_id:
        kwargs["agent_id"] = agent_id
    if app_id:
        kwargs["app_id"] = app_id
    if run_id:
        kwargs["run_id"] = run_id

    # Request enough items to cover the requested page window.
    effective_limit = 100  # Mem0 default
    if page is not None and page_size is not None:
        effective_limit = page * page_size
    elif page_size is not None:
        effective_limit = page_size

    raw = memory_client.get_all(**kwargs, limit=effective_limit)
    # get_all returns {"results": [...]} in v1.1 — unwrap to plain list.
    results = raw.get("results", raw) if isinstance(raw, dict) else raw
    # Apply pagination manually since mem0 OSS doesn't support it natively.
    if isinstance(results, list):
        if page is not None and page_size is not None:
            start = (page - 1) * page_size
            results = results[start:start + page_size]
        elif page_size is not None:
            results = results[:page_size]
    return results


@app.get("/v1/memories/{memory_id}/")
async def get_memory(memory_id: str):
    _require_memory()
    result = memory_client.get(memory_id)
    if not result:
        raise HTTPException(status_code=404, detail="memory not found")
    return result


@app.put("/v1/memories/{memory_id}/")
async def update_memory(memory_id: str, request: Request):
    _require_memory()
    body = await request.json()
    text = body.get("text", "")
    if not text:
        raise HTTPException(status_code=400, detail="text is required")
    memory_client.update(memory_id, text)
    return {"status": "updated"}


@app.delete("/v1/memories/{memory_id}/")
async def delete_memory(memory_id: str):
    _require_memory()
    memory_client.delete(memory_id)
    return {"status": "deleted"}


@app.delete("/v1/memories/")
async def delete_all_memories(request: Request):
    _require_memory()
    body = await request.json()
    kwargs = {}
    for key in ("user_id", "agent_id", "app_id", "run_id"):
        if key in body and body[key]:
            kwargs[key] = body[key]
    if not kwargs:
        raise HTTPException(status_code=400, detail="at least one scope filter required")
    memory_client.delete_all(**kwargs)
    return {"status": "deleted"}


# ── Entities ─────────────────────────────────────────────────────────────────────

@app.get("/v1/entities/")
async def list_entities():
    _require_memory()
    # Mem0 OSS doesn't have a direct list_entities; return empty for now.
    # The cloud API supports this but self-hosted may not.
    try:
        raw = memory_client.get_all()
        users = raw.get("results", raw) if isinstance(raw, dict) else raw
        return {"results": users if isinstance(users, list) else []}
    except Exception:
        return {"results": []}


@app.delete("/v1/entities/{entity_type}/{entity_id}/")
async def delete_entity(entity_type: str, entity_id: str):
    _require_memory()
    kwargs = {f"{entity_type}_id": entity_id}
    memory_client.delete_all(**kwargs)
    return {"status": "deleted"}


# ── Main ─────────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    port = int(os.environ.get("MEM0_PORT", "8010"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")
