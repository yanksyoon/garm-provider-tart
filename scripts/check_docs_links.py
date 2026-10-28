#!/usr/bin/env python3
"""Check internal Markdown links and anchors under a docs tree.

Usage:
    python3 scripts/check_docs_links.py [docs-dir]

Checks, offline:
  * every inline link/image target `[text](target)` that points at a local file
    resolves to an existing file (relative to the file containing the link);
  * an `#anchor` on a local target names a heading present in that file.

External `http(s)://` links are reported as unchecked and never fail the run, so
the check works without network access.

Exit code 0 when no broken local link or anchor is found, 1 otherwise.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

INLINE_LINK = re.compile(r"(!?\[[^\]]*\]\()([^)\s]+)(\))")


def slugify(heading: str) -> str:
    """Approximate GitHub's heading-anchor algorithm for the headings this
    project writes (ASCII, occasional inline code and punctuation)."""
    text = heading.strip().lower()
    text = re.sub(r"[`*_]", "", text)  # drop inline-code/emphasis markers
    text = re.sub(r"[^\w\s-]", "", text)  # drop punctuation
    return re.sub(r"\s+", "-", text)


def headings_of(path: Path) -> set[str]:
    """Return the set of anchors a file generates, or None if unreadable."""
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return set()
    anchors: set[str] = set()
    for line in text.splitlines():
        m = re.match(r"^(#{1,6})\s+(.+?)\s*$", line)
        if m:
            anchors.add(slugify(m.group(2)))
    return anchors


def is_external(target: str) -> bool:
    return target.startswith(("http://", "https://", "mailto:", "<"))


def main(argv: list[str]) -> int:
    docs = Path(argv[1]) if len(argv) > 1 else Path("docs")
    if not docs.is_dir():
        print(f"check_docs_links: {docs} is not a directory")
        return 1

    md_files = sorted(docs.rglob("*.md"))
    if not md_files:
        print(f"check_docs_links: no markdown under {docs}")
        return 1

    errors: list[str] = []
    external: set[str] = set()

    for md in md_files:
        text = md.read_text(encoding="utf-8")
        for m in INLINE_LINK.finditer(text):
            target = m.group(2)
            if target.startswith("#"):
                if slugify(target[1:]) not in headings_of(md):
                    errors.append(f"{md}: anchor '#{target[1:]}' not found")
                continue
            if is_external(target):
                external.add(target)
                continue
            # local target: split off the fragment
            path_part, _, anchor = target.partition("#")
            if path_part == "":
                continue
            resolved = (md.parent / path_part).resolve()
            if not resolved.exists():
                errors.append(f"{md}: broken link -> {target} (no such file)")
                continue
            if anchor and resolved.is_file() and resolved.suffix == ".md":
                if slugify(anchor) not in headings_of(resolved):
                    errors.append(
                        f"{md}: broken anchor '#{anchor}' in {resolved}"
                    )

    if external:
        print(f"check_docs_links: {len(external)} external link(s) unchecked")
        for url in sorted(external):
            print(f"  {url}")

    if errors:
        print(f"check_docs_links: {len(errors)} problem(s):")
        for err in errors:
            print(f"  {err}")
        return 1

    print(f"check_docs_links: {len(md_files)} file(s), no broken local links")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
