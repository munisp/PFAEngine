"""Lake I/O abstraction — Apache OpenDAL when LAKE_URI is set, local FS otherwise.

The lakehouse (extract_full.py / cdc_consumer.py / run.py) historically
read and wrote bronze parquet via plain local paths. This module adds a
thin adapter so the SAME code can target any OpenDAL-backed object store:

    LAKE_URI=s3://bucket/lake        # S3 (S3_ENDPOINT/S3_REGION/S3_ACCESS_KEY/...)
    LAKE_URI=fs:///lake              # explicit local filesystem root
    LAKE_URI=memory://               # in-memory (tests)
    LAKE_URI=gcs://bucket/lake       # GCS (GCS_CREDENTIAL)
    LAKE_URI=azblob://container/lake # Azure Blob (AZBLOB_ACCOUNT_*)
    (unset)                          # local FS at --lake (unchanged behaviour)

All lake-relative paths are POSIX strings like
"bronze/member/dt=2024-06-01/part-full.parquet". The adapter deliberately
exposes only the handful of operations the lakehouse needs
(write/read/exists/glob/list_dirs/remove_dir/replace + parquet helpers).

Caveats vs the local backend:
  * Object stores have atomic single-object PUT, so the tmp+rename dance
    degrades to rename-when-supported / copy+delete otherwise.
  * DuckDB (run.py silver/gold) still reads parquet from a LOCAL path, so
    model builds require an fs-backed or local lake; extraction and CDC
    writes work against any backend. local_root() raises otherwise.
"""
from __future__ import annotations

import fnmatch
import io
import logging
import os
import shutil
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

log = logging.getLogger("analytics.lakeio")


def open_lake(lake) -> "LakeIO":
    """Resolve `lake` (str | Path | LakeIO) to a LakeIO.

    LAKE_URI takes precedence when set; a `lake` argument that already
    looks like a URI (contains "://") is honoured too. Otherwise this is
    the local-filesystem fallback, byte-for-byte the old behaviour.
    """
    if isinstance(lake, LakeIO):
        return lake
    uri = os.environ.get("LAKE_URI", "").strip()
    if not uri and isinstance(lake, str) and "://" in lake:
        uri = lake
    if uri:
        return OpenDALLake(uri)
    return LocalLake(Path(lake))


def _check_rel(rel: str) -> str:
    """Fail-closed guard on lake-relative paths (traversal defence)."""
    if not rel or rel.startswith(("/", "\\")) or "\\" in rel:
        raise ValueError(f"unsafe lake path: {rel!r}")
    if any(seg in ("", ".", "..") for seg in rel.split("/")):
        raise ValueError(f"unsafe lake path: {rel!r}")
    return rel


class LakeIO:
    """Interface shared by both backends."""

    is_remote = False

    # -- bytes ----------------------------------------------------------
    def write_bytes(self, rel: str, data: bytes) -> None:
        raise NotImplementedError

    def read_bytes(self, rel: str) -> bytes:
        raise NotImplementedError

    def exists(self, rel: str) -> bool:
        raise NotImplementedError

    def delete_file(self, rel: str) -> None:
        raise NotImplementedError

    # -- dirs / listing ---------------------------------------------------
    def glob(self, pattern: str) -> list[str]:
        """Relative paths of FILES matching a fnmatch pattern (sorted)."""
        raise NotImplementedError

    def list_dirs(self, rel: str) -> list[str]:
        """Immediate subdirectory names under rel (sorted)."""
        raise NotImplementedError

    def dir_exists(self, rel: str) -> bool:
        raise NotImplementedError

    def remove_dir(self, rel: str) -> None:
        """Recursively remove rel (missing is a no-op)."""
        raise NotImplementedError

    def replace(self, src_rel: str, dst_rel: str) -> None:
        raise NotImplementedError

    # -- conveniences -----------------------------------------------------
    def write_text(self, rel: str, text: str) -> None:
        self.write_bytes(rel, text.encode("utf-8"))

    def read_text(self, rel: str) -> str:
        return self.read_bytes(rel).decode("utf-8")

    def write_parquet(self, tbl: "pa.Table", rel: str) -> None:
        buf = io.BytesIO()
        pq.write_table(tbl, buf)
        self.write_bytes(rel, buf.getvalue())

    def read_parquet(self, rel: str) -> "pa.Table":
        return pq.read_table(io.BytesIO(self.read_bytes(rel)))

    def local_root(self) -> Path:
        """Local filesystem root for DuckDB; raises for remote lakes."""
        raise RuntimeError(
            "LAKE_URI lake has no local path — DuckDB silver/gold builds "
            "need a local or fs:// lake (extraction/CDC work against any "
            "backend)")

    def __repr__(self) -> str:  # pragma: no cover
        return f"{type(self).__name__}(...)"


class LocalLake(LakeIO):
    """Local filesystem passthrough — the pre-OpenDAL behaviour."""

    def __init__(self, root: Path):
        self.root = Path(root)

    def _p(self, rel: str) -> Path:
        return self.root / _check_rel(rel)

    def write_bytes(self, rel: str, data: bytes) -> None:
        p = self._p(rel)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(data)

    def read_bytes(self, rel: str) -> bytes:
        return self._p(rel).read_bytes()

    def exists(self, rel: str) -> bool:
        return self._p(rel).exists()

    def delete_file(self, rel: str) -> None:
        self._p(rel).unlink(missing_ok=True)

    def glob(self, pattern: str) -> list[str]:
        return sorted(str(p.relative_to(self.root))
                      for p in self.root.glob(pattern) if p.is_file())

    def list_dirs(self, rel: str) -> list[str]:
        base = self._p(rel)
        if not base.is_dir():
            return []
        return sorted(p.name for p in base.iterdir() if p.is_dir())

    def dir_exists(self, rel: str) -> bool:
        return self._p(rel).is_dir()

    def remove_dir(self, rel: str) -> None:
        p = self._p(rel)
        if p.exists():
            shutil.rmtree(p)

    def replace(self, src_rel: str, dst_rel: str) -> None:
        dst = self._p(dst_rel)
        dst.parent.mkdir(parents=True, exist_ok=True)
        os.replace(self._p(src_rel), dst)

    def write_parquet(self, tbl: "pa.Table", rel: str) -> None:
        p = self._p(rel)
        p.parent.mkdir(parents=True, exist_ok=True)
        pq.write_table(tbl, p)

    def read_parquet(self, rel: str) -> "pa.Table":
        return pq.read_table(self._p(rel))

    def local_root(self) -> Path:
        return self.root


class OpenDALLake(LakeIO):
    """OpenDAL-backed lake (LAKE_URI). Requires the `opendal` package."""

    is_remote = True

    def __init__(self, uri: str):
        try:
            import opendal
        except ImportError as exc:  # pragma: no cover — env dependent
            raise RuntimeError(
                "LAKE_URI is set but the `opendal` package is not installed "
                "(pip install opendal)") from exc
        scheme, root, kwargs = self._parse(uri)
        self._scheme = scheme
        self._fs_root: Path | None = kwargs.pop("_fs_root", None)
        self.op = opendal.Operator(scheme, **kwargs)
        if root:
            # opendal roots are absolute-style; keep objects under it.
            self._prefix = root.strip("/")
        else:
            self._prefix = ""
        log.info("lake: OpenDAL backend %s (prefix=%r)", scheme, self._prefix)

    @staticmethod
    def _parse(uri: str) -> tuple[str, str, dict]:
        """LAKE_URI -> (scheme, root-prefix, operator kwargs)."""
        if "://" not in uri:
            raise ValueError(f"LAKE_URI must be scheme://..., got {uri!r}")
        scheme, rest = uri.split("://", 1)
        kwargs: dict = {}
        if scheme == "memory":
            return scheme, "", kwargs
        if scheme == "fs":
            # fs:///abs/path or fs://rel/path
            path = rest if rest.startswith("/") else "/" + rest.lstrip("/")
            kwargs["root"] = path
            kwargs["_fs_root"] = Path(path)
            return scheme, "", kwargs
        if scheme == "s3":
            bucket, _, prefix = rest.partition("/")
            kwargs["bucket"] = bucket
            for env_key, opt in (("S3_ENDPOINT", "endpoint"),
                                 ("S3_REGION", "region"),
                                 ("S3_ACCESS_KEY", "access_key_id"),
                                 ("S3_SECRET_KEY", "secret_access_key")):
                v = os.environ.get(env_key)
                if v:
                    kwargs[opt] = v
            return scheme, prefix, kwargs
        if scheme == "gcs":
            bucket, _, prefix = rest.partition("/")
            kwargs["bucket"] = bucket
            cred = os.environ.get("GCS_CREDENTIAL") or os.environ.get(
                "GOOGLE_APPLICATION_CREDENTIALS")
            if cred:
                kwargs["credential_path" if Path(cred).exists()
                       else "credential"] = cred
            return scheme, prefix, kwargs
        if scheme == "azblob":
            container, _, prefix = rest.partition("/")
            kwargs["container"] = container
            for env_key, opt in (("AZBLOB_ACCOUNT_NAME", "account_name"),
                                 ("AZBLOB_ACCOUNT_KEY", "account_key"),
                                 ("AZBLOB_ENDPOINT", "endpoint")):
                v = os.environ.get(env_key)
                if v:
                    kwargs[opt] = v
            return scheme, prefix, kwargs
        raise ValueError(f"unsupported LAKE_URI scheme {scheme!r} "
                         "(want fs|memory|s3|gcs|azblob)")

    def _k(self, rel: str) -> str:
        rel = _check_rel(rel)
        return f"{self._prefix}/{rel}" if self._prefix else rel

    def _unkey(self, key: str) -> str:
        if self._prefix:
            assert key.startswith(self._prefix + "/"), key
            return key[len(self._prefix) + 1:]
        return key

    def write_bytes(self, rel: str, data: bytes) -> None:
        self.op.write(self._k(rel), data)

    def read_bytes(self, rel: str) -> bytes:
        return bytes(self.op.read(self._k(rel)))

    def exists(self, rel: str) -> bool:
        try:
            return bool(self.op.exists(self._k(rel)))
        except Exception:  # backend without exists capability
            try:
                self.op.stat(self._k(rel))
                return True
            except Exception:
                return False

    def delete_file(self, rel: str) -> None:
        try:
            self.op.delete(self._k(rel))
        except Exception as exc:
            if "NotFound" not in type(exc).__name__:
                raise

    def glob(self, pattern: str) -> list[str]:
        keys = self._all_keys()
        return sorted(k for k in keys if fnmatch.fnmatchcase(k, pattern))

    def _all_keys(self) -> list[str]:
        base = (self._prefix + "/") if self._prefix else ""
        out = []
        for entry in self.op.scan(base or "/"):
            p = entry.path.rstrip("/")
            if p:
                out.append(self._unkey(p))
        return out

    def list_dirs(self, rel: str) -> list[str]:
        prefix = self._k(rel) + "/"
        names = set()
        for entry in self.op.scan(prefix):
            rest = entry.path[len(prefix):] if entry.path.startswith(prefix) else None
            if rest and "/" in rest.strip("/"):
                names.add(rest.strip("/").split("/", 1)[0])
        return sorted(names)

    def dir_exists(self, rel: str) -> bool:
        prefix = self._k(rel) + "/"
        for _ in self.op.scan(prefix):
            return True
        return False

    def remove_dir(self, rel: str) -> None:
        if self.dir_exists(rel):
            self.op.remove_all(self._k(rel) + "/")

    def replace(self, src_rel: str, dst_rel: str) -> None:
        src, dst = self._k(src_rel), self._k(dst_rel)
        try:
            self.op.rename(src, dst)
            return
        except Exception:
            pass  # backend without rename — copy+delete
        self.op.write(dst, self.op.read(src))
        self.op.delete(src)

    def local_root(self) -> Path:
        if self._fs_root is not None:
            return self._fs_root
        return super().local_root()
