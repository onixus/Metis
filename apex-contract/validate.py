#!/usr/bin/env python3
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "apex-contract" / "manifest.json"
PIN = "878d138c560cd4106ab0d6cccde804ddc3e5ae1d"

def fail(message: str) -> None:
    raise SystemExit(f"APEX contract validation failed: {message}")

def main() -> None:
    doc = json.loads(MANIFEST.read_text(encoding="utf-8"))
    if doc.get("apex_contract_version") != "1.0":
        fail("contract version drift")
    canonical = doc.get("canonical", {})
    if canonical.get("repo") != "onixus/unified-platform" or canonical.get("commit") != PIN:
        fail("canonical contract pin drift")
    if doc.get("system") != "metis" or doc.get("namespace") != "metis":
        fail("system/namespace drift")
    identity = doc.get("identity", {})
    if identity.get("trust_unsigned_role_header") is not False:
        fail("unsigned role headers must never be trusted")
    if identity.get("owning_service_authorizes_mutations") is not True:
        fail("Metis must authorize its own mutations")
    ownership = doc.get("ownership", {})
    if ownership.get("gateway_is_source_of_truth") is not False:
        fail("Gateway must not own Metis domain state")
    if ownership.get("clickhouse_is_transactional_source") is not False:
        fail("ClickHouse must remain a projection")
    for guard in doc.get("source_guards", []):
        path = ROOT / guard["path"]
        if not path.exists():
            fail(f"missing guarded source: {guard['path']}")
        text = path.read_text(encoding="utf-8")
        for needle in guard.get("contains", []):
            if needle not in text:
                fail(f"{guard['path']} lost marker: {needle}")
    openapi = (ROOT / "api/openapi.yaml").read_text(encoding="utf-8")
    if "url: /api/v1" not in openapi:
        fail("integration API is not major-versioned")
    print("APEX Architecture Contract v1: metis OK")

if __name__ == "__main__":
    main()
