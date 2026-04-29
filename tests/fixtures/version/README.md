# Version-gate test fixtures

Test data for `lib/hooks/git/pre-push-version-gate.sh` classification
logic. Each fixture under `change-classification/` is a file-path
list (one path per line) representing a hypothetical diff. The
fixture's filename is the expected classification.

## Files

| Fixture | Expected class | Required bump |
|---|---|---|
| `doc-only.txt` | DOC_ONLY | none |
| `patch-refactor.txt` | PATCH_REFACTOR | patch |
| `patch-refinement.txt` | PATCH_REFINEMENT | patch |
| `default-patch.txt` | DEFAULT_PATCH | patch |
| `minor-additive.txt` | MINOR_ADDITIVE | minor |
| `major-breaking.txt` | MAJOR_BREAKING | major |

## How to use

The fixtures are read by `tests/run-version-gate-fixtures.sh` (when
written; v0.3+) which sources the gate hook's `classify_file`
function and asserts each path classifies to its filename's tier.

Until the runner exists, these fixtures serve as a documented
classification reference: any change to the gate's path-matching
rules should preserve these expected classifications.

## Naming convention

Fixture filename = `<expected-class-lowercased-with-hyphens>.txt`.
Example: `MINOR_ADDITIVE` → `minor-additive.txt`.
