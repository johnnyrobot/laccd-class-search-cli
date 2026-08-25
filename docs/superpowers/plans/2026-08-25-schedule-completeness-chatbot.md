# LACCD Chatbot Schedule Completeness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve every uniquely identifiable primary and related class component in chatbot schedule data while keeping voice responses concise and explicitly complete about course coverage.

**Architecture:** Introduce one recursive schedule traversal shared by live chat and sequencing. Normalize every component into the existing response dictionary with two additive fields, update voice disclosure, and compare chatbot output with the CLI and raw replay oracle.

**Tech Stack:** Python 3, FastAPI, httpx, pytest 8.4.1, LiveKit voice tools, Android/Kotlin/Moshi for consumer compatibility.

**Spec:** `/Users/laccd/code/cli-tools/laccd-class-search-cli/docs/superpowers/specs/2026-08-25-schedule-completeness-design.md`

## Global Constraints

- Execute in an isolated `laccd_chatbot` worktree created with `superpowers:using-git-worktrees`; preserve untracked `dogfood-output/` and `jina.txt` in the primary checkout.
- Start from `main` commit `3cd93ac` or its current reviewed descendant.
- Do not make the chatbot invoke the CLI at runtime; the CLI is used only by verification.
- Preserve existing schedule dictionary keys and add only `component` and `related_to`.
- Skip a blank primary and its subtree; skip a blank related node but continue valid descendants under the original primary ancestor.
- Keep the eight-detail voice cap, but disclose total distinct courses, total components, and omitted detail.
- Use recorded fixtures in tests; never hit live services from pytest.
- Every Python command uses `PYTHONPATH=backend:.` and `.venv/bin/python3`.
- Deployment is out of scope; hand off an exact verified revision to the existing chatbot deployment task.

## File Map

- Create `backend/app/services/schedule_sections.py`: shared recursive traversal and class-number parsing.
- Create `tests/test_schedule_sections.py`: traversal, dedupe, blank, and ancestry tests.
- Modify `backend/app/services/live_schedule.py` and `tests/test_chat_live_schedule.py`: emit related components and additive keys.
- Modify `backend/app/services/sequencing/cache.py` and `tests/test_sequencing_cache.py`: consume shared traversal.
- Modify `voice_agent/tools.py`; create `tests/test_voice_schedule_completeness.py`: course/component disclosure.
- Modify `mobile/app/src/main/kotlin/com/laccd/chatbot/tools/LaccdApi.kt`; create `LaccdApiScheduleCompatibilityTest.kt`: additive-field consumer contract.
- Modify `docs/classscheduleapi.md`: normalized completeness and presentation contract.
- Create `scripts/verify_schedule_completeness_replay.py` and `tests/test_schedule_completeness_replay.py`: cross-repository oracle comparison.

---

### Task 1: Shared Recursive Schedule Traversal

**Files:**
- Create: `backend/app/services/schedule_sections.py`
- Create: `tests/test_schedule_sections.py`

**Interfaces:**
- Produces `normalize_class_number(raw: object) -> tuple[str, str]` returning `(class_number, component)`.
- Produces `iter_schedule_sections(listing: dict) -> Iterator[tuple[dict, dict, str]]` returning `(course, section, primary_class_number)`.

- [ ] **Step 1: Write failing traversal tests**

Use small dictionaries to assert primary-before-descendant order, top-primary ancestry at two related levels, first-record dedupe per course, same class number retained in different courses, malformed raw preservation, blank related skip with descendant continuation, and blank primary subtree skip.

```python
def test_iter_schedule_sections_carries_top_primary_to_nested_descendants(caplog):
    listing = fixture_with_nested_related("10001 (LEC)", "10002 (LAB)", "10003 (LAB)")
    rows = list(iter_schedule_sections(listing))
    assert [(normalize_class_number(s["classNbr"])[0], primary) for _, s, primary in rows] == [
        ("10001", "10001"), ("10002", "10001"), ("10003", "10001"),
    ]
```

- [ ] **Step 2: Run RED**

Run: `PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_schedule_sections.py -q`

Expected: import failure because the module does not exist.

- [ ] **Step 3: Implement the iterator**

Implement the exact signatures:

```python
logger = logging.getLogger(__name__)
_CLASS_COMPONENT_RE = re.compile(r"^([^\s()]+)\s+\(([A-Za-z]+)\)$")

def normalize_class_number(raw: object) -> tuple[str, str]:
    value = str(raw or "").strip()
    match = _CLASS_COMPONENT_RE.fullmatch(value)
    return (match.group(1), match.group(2).upper()) if match else (value, "")

def iter_schedule_sections(listing: dict) -> Iterator[tuple[dict, dict, str]]:
    for subject in listing.get("subjects") or []:
        for course in subject.get("courses") or []:
            seen: set[str] = set()
            def walk(section: dict, primary: str):
                number, _component = normalize_class_number(section.get("classNbr"))
                if not number:
                    logger.warning("schedule component has blank classNbr")
                    for child in section.get("relsections") or []:
                        yield from walk(child, primary)
                    return
                if number in seen:
                    logger.warning("duplicate schedule class number %s", number)
                    for child in section.get("relsections") or []:
                        yield from walk(child, primary)
                    return
                seen.add(number)
                yield course, section, primary
                for child in section.get("relsections") or []:
                    yield from walk(child, primary)
            for primary_section in course.get("sections") or []:
                primary, _component = normalize_class_number(primary_section.get("classNbr"))
                if not primary:
                    logger.warning("primary schedule component has blank classNbr; skipping subtree")
                    continue
                yield from walk(primary_section, primary)
```

Within each course, keep a `seen` set. Log duplicate/blank warnings. A blank primary suppresses its full subtree. For related recursion, a blank node is skipped but its children recurse with the original non-blank primary.

- [ ] **Step 4: Run GREEN**

Run the Step 2 command; expect all tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/schedule_sections.py tests/test_schedule_sections.py
git commit -m "feat: share complete schedule traversal"
```

### Task 2: Emit Every Component from Live Schedule Formatting

**Files:**
- Modify: `backend/app/services/live_schedule.py`
- Modify: `tests/test_chat_live_schedule.py`
- Create: `tests/fixtures/lamc_2268_multimd_sample.json`

**Interfaces:**
- Consumes Task 1 iterator/normalizer.
- Preserves `format_sections(listing, course_number=None, keyword=None) -> list[dict]`.
- Adds dictionary keys `component` and `related_to`.

- [ ] **Step 1: Add failing formatter tests**

Record a trimmed MULTIMD 100 fixture with one LEC and one linked LAB. Assert:

```python
sections = format_sections(fixture)
assert [s["class_number"] for s in sections] == ["13191", "13192"]
assert sections[0]["component"] == "LEC" and sections[0]["related_to"] == ""
assert sections[1]["component"] == "LAB" and sections[1]["related_to"] == "13191"
```

Also assert course-number and keyword filters retain all related components for a matched course, ZTC filtering uses each component's own `class_type`, and `search_live_schedule.total_sections` equals the complete component count.

- [ ] **Step 2: Run RED**

Run: `PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_chat_live_schedule.py -q`

Expected: linked LAB/additive-field assertions fail.

- [ ] **Step 3: Refactor `format_sections` onto the iterator**

Filter at the course level, then render every yielded section. Use its first meeting or TBA defaults. Set `related_to=""` when `primary_class_number == class_number`; otherwise use the primary. Preserve all existing keys and values.

```python
for course, section, primary in iter_schedule_sections(listing):
    class_number, component = normalize_class_number(section.get("classNbr"))
    meeting = (section.get("meetings") or [{}])[0]
    rows.append({
        "course_code": f"{course.get('subject', '')} {course.get('catalogNbr', '')}",
        "course_title": course.get("descr", ""), "class_number": class_number,
        "section": section.get("classNbr", ""), "days": meeting.get("days", "TBA"),
        "time": meeting.get("times", "TBA"), "location": meeting.get("room", "TBA"),
        "instructor": meeting.get("instr", "TBA"), "dates": section.get("dates", ""),
        "class_type": section.get("classType", []), "component": component,
        "related_to": "" if class_number == primary else primary,
    })
```

- [ ] **Step 4: Run formatter/routes tests**

```bash
PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_chat_live_schedule.py tests/test_mobile_tools_routes.py -q
```

Expected: pass with complete totals.

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/live_schedule.py tests/test_chat_live_schedule.py tests/fixtures/lamc_2268_multimd_sample.json
git commit -m "fix: include related schedule components"
```

### Task 3: Remove Sequencing Traversal Drift

**Files:**
- Modify: `backend/app/services/sequencing/cache.py`
- Modify: `tests/test_sequencing_cache.py`

**Interfaces:**
- Consumes `iter_schedule_sections` from Task 1.
- Preserves `_iter_sections(listing)` compatibility for current tests/callers.

- [ ] **Step 1: Write a failing delegation/edge test**

Extend the existing 14-record fixture test with a nested related component and assert `_iter_sections` includes it with the same course code. Add a monkeypatch test proving `cache._iter_sections` delegates to the shared iterator rather than keeping a second traversal.

- [ ] **Step 2: Run RED**

Run: `PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_sequencing_cache.py -q`

Expected: nested/delegation assertion fails.

- [ ] **Step 3: Replace local traversal**

Keep the compatibility generator but implement it only as:

```python
def _iter_sections(listing: dict):
    for course, section, _primary in iter_schedule_sections(listing):
        subject = str(course.get("subject") or "").strip()
        catalog = str(course.get("catalogNbr") or "").strip()
        yield (f"{subject} {catalog}" if subject and catalog else ""), section
```

- [ ] **Step 4: Run GREEN**

Run the Step 2 command; expect pass and unchanged rollup schema.

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/sequencing/cache.py tests/test_sequencing_cache.py
git commit -m "refactor: share schedule section traversal"
```

### Task 4: Make Voice Coverage Explicit

**Files:**
- Modify: `voice_agent/tools.py`
- Create: `tests/test_voice_schedule_completeness.py`

**Interfaces:**
- Preserves `_fmt_sections_voice(sections: list[dict], limit: int = 8) -> str`.
- Produces helper `_schedule_coverage_voice(sections: list[dict], course_limit: int = 12) -> str`.

- [ ] **Step 1: Write failing voice tests**

Build 26 components across the 10 expected MULTIMD course codes. Assert output includes `10 courses`, `26 components`, every distinct code, details for exactly 8 components, and `18 additional components`. Add a 13-course test asserting only the first 12 codes and `1 additional course`.

- [ ] **Step 2: Run RED**

Run: `PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_voice_schedule_completeness.py -q`

Expected: count/course disclosure missing.

- [ ] **Step 3: Implement first-occurrence course disclosure**

Deduplicate non-empty `course_code` values in input order. Prepend the coverage sentence/list to the existing eight-detail rendering. Preserve the existing official-site direction but change “sections” to “components” for the omitted count.

```python
def _schedule_coverage_voice(sections: list[dict], course_limit: int = 12) -> str:
    courses = list(dict.fromkeys(s.get("course_code", "") for s in sections if s.get("course_code")))
    shown = courses[:course_limit]
    return f"{len(courses)} courses and {len(sections)} components: {', '.join(shown)}"
```

- [ ] **Step 4: Run voice tests**

```bash
PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_voice_schedule_completeness.py tests/test_chat_live_schedule.py -q
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add voice_agent/tools.py tests/test_voice_schedule_completeness.py
git commit -m "fix: disclose complete voice schedule coverage"
```

### Task 5: Prove Mobile Additive-Key Compatibility

**Files:**
- Modify: `mobile/app/src/main/kotlin/com/laccd/chatbot/tools/LaccdApi.kt`
- Create: `mobile/app/src/test/kotlin/com/laccd/chatbot/tools/LaccdApiScheduleCompatibilityTest.kt`

**Interfaces:**
- Extends `ScheduleSection` with optional/defaulted `component` and `relatedTo` fields.

- [ ] **Step 1: Write a failing Moshi decode test**

Decode a schedule response containing all backend keys plus `component` and `related_to`. Assert the section decodes, the two fields are retained, and unrelated unknown keys remain ignored.

- [ ] **Step 2: Run RED**

Run from `mobile/`: `./gradlew :app:testDebugUnitTest --tests '*LaccdApiScheduleCompatibilityTest'`

Expected: missing data-class properties.

- [ ] **Step 3: Add defaulted fields**

```kotlin
val component: String = "",
@Json(name = "related_to") val relatedTo: String = "",
```

Do not make either required and do not change existing serialized names.

- [ ] **Step 4: Run GREEN**

Run the Step 2 command; expect pass.

- [ ] **Step 5: Commit**

```bash
git add mobile/app/src/main/kotlin/com/laccd/chatbot/tools/LaccdApi.kt mobile/app/src/test/kotlin/com/laccd/chatbot/tools/LaccdApiScheduleCompatibilityTest.kt
git commit -m "test: preserve schedule component metadata on mobile"
```

### Task 6: Document Chatbot Schedule Completeness

**Files:**
- Modify: `docs/classscheduleapi.md`

- [ ] **Step 1: Update schema and flow documentation**

Document recursive `relsections`, top-primary `related_to`, `component`, dedupe/blank rules, complete text/mobile totals, and the separate voice presentation cap.

- [ ] **Step 2: Validate documentation**

```bash
rg -n "relsections|related_to|component|eight|12" docs/classscheduleapi.md
git diff --check
```

Expected: all required terms present and no whitespace errors.

- [ ] **Step 3: Commit**

```bash
git add docs/classscheduleapi.md
git commit -m "docs: define complete schedule normalization"
```

### Task 7: Cross-Repository Replay Verification

**Files:**
- Create: `scripts/verify_schedule_completeness_replay.py`
- Create: `tests/test_schedule_completeness_replay.py`

**Interfaces:**
- Consumes the CLI plan's `tools/schedule-replay` command and fixture manifest.
- Produces a JSON verification report with CLI/chatbot/oracle sets and fixture hashes.

- [ ] **Step 1: Write a failing orchestration test**

Point the script at a fake CLI executable returning a mismatched set and assert non-zero; then a matching set and assert zero. The chatbot oracle must be computed recursively from the exact discovery JSON bytes via `format_sections`.

- [ ] **Step 2: Run RED**

Run: `PYTHONPATH=backend:. .venv/bin/python3 -m pytest tests/test_schedule_completeness_replay.py -q`

Expected: missing script failure.

- [ ] **Step 3: Implement the verifier**

Accept `--cli-repo`, `--chatbot-repo`, and `--fixtures`. Invoke `go run ./tools/schedule-replay` in the CLI repo, load identical discovery JSON for chatbot normalization, hash every fixture, compare sorted unique class-number sets with the recursive raw oracle, and print a self-contained JSON report. Exit non-zero on any mismatch.

```python
def verify(cli_repo: Path, chatbot_repo: Path, fixtures: Path) -> dict:
    cli_report = run_cli_replay(cli_repo, fixtures)
    chatbot_numbers = sorted({s["class_number"] for s in format_sections(load_discovery(fixtures))})
    oracle_numbers = recursive_raw_oracle(load_discovery(fixtures))
    if cli_report["class_numbers"] != oracle_numbers or chatbot_numbers != oracle_numbers:
        raise SystemExit(1)
    return {"cli": cli_report, "chatbot": chatbot_numbers, "oracle": oracle_numbers}
```

- [ ] **Step 4: Run deterministic cross-repo replay**

```bash
PYTHONPATH=backend:. .venv/bin/python3 scripts/verify_schedule_completeness_replay.py \
  --cli-repo /absolute/path/to/cli-worktree \
  --chatbot-repo "$PWD" \
  --fixtures /absolute/path/to/cli-worktree/internal/laccd/testdata/completeness
```

Expected: CLI set = chatbot set = raw oracle, with matching fixture hashes.

- [ ] **Step 5: Commit**

```bash
git add scripts/verify_schedule_completeness_replay.py tests/test_schedule_completeness_replay.py
git commit -m "test: verify cross-repo schedule completeness"
```

### Task 8: Chatbot Verification and Deployment Handoff

**Files:** Verify only; repair failures in the owning task.

- [ ] **Step 1: Run targeted backend tests**

```bash
PYTHONPATH=backend:. .venv/bin/python3 -m pytest \
  tests/test_schedule_sections.py \
  tests/test_chat_live_schedule.py \
  tests/test_sequencing_cache.py \
  tests/test_voice_schedule_completeness.py \
  tests/test_mobile_tools_routes.py \
  tests/test_schedule_completeness_replay.py -q
```

Expected: all pass.

- [ ] **Step 2: Run the full backend suite**

Run: `PYTHONPATH=backend:. .venv/bin/python3 -m pytest`

Expected: exit 0; list and evaluate skips rather than quoting stale HANDOFF counts.

- [ ] **Step 3: Run mobile compatibility test**

Run from `mobile/`: `./gradlew :app:testDebugUnitTest --tests '*LaccdApiScheduleCompatibilityTest'`

Expected: exit 0.

- [ ] **Step 4: Run deterministic replay and fresh live smoke**

Run Task 7's replay. Then use a fresh Python process with `_listing_cache` empty to query LAMC `2268` for MULTIMD/CINEMA/PHOTO. Record UTC time, both repository revisions, raw-response hashes, nearest-oracle timing skew, component/course counts, and CINEMA 125/PHOTO 015/121 presence. Do not call these independent live requests byte-identical.

- [ ] **Step 5: Inspect repository state and commit final repairs**

Run `git diff --check`, `git status --short`, and `git log --oneline` for the feature range. Commit only planned repairs; leave the worktree clean.

- [ ] **Step 6: Prepare deployment instruction, but do not deploy**

Provide the user with the exact chatbot revision, CLI revision, test/replay/live evidence, caveats, and this instruction for the existing chatbot task: deploy only the named chatbot revision to the named environment, then run its standard health check and a production LAMC Fall 2026 Multimedia query that reports complete course/component coverage.
