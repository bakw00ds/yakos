# Domain 6: Regulated-Data Handling

**Goal:** Assess handling of regulated and sensitive data against
applicable frameworks (HIPAA, GDPR, CCPA/CPRA, SOC 2, contract-bound
engagement data) and against the baseline expectation of "users
reasonably expect this protected."

This is the highest-stakes domain in any release-audit run.

This playbook is invoked by project-specific privacy-review agents
(v0.2: `privacy-reviewer`) and by release-audit skills that include
the regulatory domain.

---

## Important framing

The applicable regulatory framework depends on:

1. **What kind of data the project holds.** Health, financial,
   educational, biometric, children's, location, communications —
   each has its own regulatory layer.
2. **Where users / data subjects are located.** GDPR for EU residents,
   CCPA/CPRA for California, regional laws for many jurisdictions.
3. **The project's contractual obligations.** SOC 2 attestations,
   pentest engagement NDAs, B2B data-processing agreements.
4. **The project's HIPAA status (if health data).** Three plausible
   states:
   - **Covered Entity** — rare unless acting as a healthcare
     provider.
   - **Business Associate** — processes health data on behalf of a
     covered entity, BAA in place.
   - **Not legally HIPAA-covered, but holds health-adjacent data
     users reasonably expect protected** — most common for
     consumer health-adjacent products.

**Record the chosen framing in the audit's `00-scope.md` and tailor
findings accordingly.** The default assumption for this playbook is
the highest-stakes plausible interpretation, with risk-management
controls scaled to actual exposure.

**This audit is not a substitute for legal counsel.** Findings
should be routed to qualified counsel before any regulatory claim is
made publicly.

---

## What counts as regulated data

For this audit, treat as sensitive:

- **Health-adjacent:** body measurements, weight, medical history,
  conditions, medications, allergies, performance/physical data,
  nutrition logs tied to health, progress photos.
- **Financial:** bank, credit card, transaction history, balances.
- **Educational:** student records, grades (FERPA-relevant in US).
- **Biometric:** fingerprints, face data, voice prints, behavioral
  signatures.
- **Children's:** anything tied to a user identified as <13 (or
  <16/<18 per jurisdiction).
- **Location:** precise GPS, frequent location patterns.
- **Communications:** messages between users, especially with
  health/legal/financial content.
- **Engagement / client work:** pentest findings, red-team
  artifacts, client-shared materials under NDA.

Combined with identifiers (name, email, phone, DOB, IP, device ID,
photo), this becomes the regulated-data set the project holds.

---

## The three control families (apply across frameworks)

The HIPAA Security Rule's organization (Administrative / Physical /
Technical) maps cleanly to most other frameworks, including SOC 2
Trust Services Criteria. Use it as the audit lens.

### Administrative

- Security officer assigned (named individual)
- Workforce training — anyone with data access has documented
  privacy training
- Access authorization — documented process for granting/revoking
- Incident response plan — documented, with regulated-data-specific
  procedures (breach notification timelines vary by framework)
- Third-party agreements — every vendor that touches regulated data
  has a BAA / DPA / equivalent, or a documented risk acceptance

### Physical

- Where is data physically stored? Cloud provider, region,
  encryption at rest
- Workstation security — laptops accessing production data directly
- Device / media disposal — policy for decommissioned servers,
  backup media, old laptops

### Technical

- Access control (unique user IDs, auto-logoff, encryption)
- Audit controls (who accessed what data when)
- Integrity (protection against unauthorized alteration)
- Transmission security (TLS everywhere)

## Automated pass

### 6.1 Data-leak scan

Grep codebase and recent logs for accidental regulated-data
exposure:

```bash
# Tune patterns to the project's data model
trufflehog filesystem . \
  --custom-detectors-path audit/regulated-data-detectors.yml \
  --json > raw/06-regulated-data/leak-scan.json

# Grep logs (use project-specific identifiers)
grep -rE '(ssn|date.of.birth|medical|diagnosis|medication|account_number|location_lat)' \
  /path/to/staging/logs > raw/06-regulated-data/log-grep.txt || true
```

Any hit in log output → **P0**.

### 6.2 Encryption verification

#### At rest (Postgres example)

```sql
SHOW ssl;
SELECT * FROM pg_stat_ssl WHERE pid = pg_backend_pid();

-- Column-level encryption check
SELECT column_name, udt_name FROM information_schema.columns
WHERE table_schema='public' AND udt_name='bytea';
```

For managed services (RDS, Cloud SQL, Supabase, etc.), verify via
provider console that encryption at rest is enabled.

Record in `raw/06-regulated-data/encryption-state.md`:

- [ ] At-rest encryption enabled, algorithm (AES-256 expected)
- [ ] Key management (provider-managed? customer-managed? rotation
  schedule?)
- [ ] TLS 1.2+ enforced for DB connections
- [ ] Backup encryption

### 6.3 Audit log coverage

For every endpoint or code path that reads/writes regulated data,
verify an audit log entry is generated.

```bash
grep -rn 'db.Query\|db.Exec\|tx.Query\|tx.Exec' --include="*.go" . \
  | grep -i '<table-names-with-regulated-data>' \
  > raw/06-regulated-data/access-sites.txt

grep -rn 'audit.Log\|logger.Audit\|auditLog' --include="*.go" . \
  > raw/06-regulated-data/audit-log-sites.txt
```

Compare. Any access site without a nearby audit log call → **P1**.

### 6.4 Third-party inventory

Enumerate every third party the app sends or could send regulated
data to:

- Cloud host
- AI APIs (if Claude, OpenAI, Gemini ever see regulated data)
- Analytics (Posthog, GA, Mixpanel)
- Error tracking (Sentry, Rollbar, Bugsnag)
- Payment processor
- Email (SendGrid, Postmark, SES)
- SMS (Twilio)
- Push notifications (Firebase)
- Backup services

For each:

- [ ] Do they receive regulated data in normal operation? (error
  payloads, user IDs linked to regulated content, message bodies)
- [ ] Is there a BAA / DPA / equivalent agreement?
- [ ] Data residency known?
- [ ] Data retention terms known?

**Common leak source:** error trackers receive full request bodies by
default. If a regulated-data-containing POST errors out, the body
goes to the tracker. **Verify scrubbing configuration.** Usually P1.

### 6.5 Data retention + deletion

- [ ] Written retention policy exists
- [ ] Code implements the policy (deletion job or mechanism present)
- [ ] User-facing data export works (§6.7)
- [ ] User-facing data deletion works and is complete (cascades,
  removes from backups per policy, removes from third parties per
  vendor agreements)

### 6.6 AI / LLM handling (where applicable)

- [ ] Is regulated data ever sent to an LLM provider?
- [ ] If yes, is there an enterprise-tier / zero-retention agreement,
  or is data de-identified first?
- [ ] Is user consent obtained for AI processing of their data?
- [ ] Prompt + response logs — are they treated as regulated data?
- [ ] Model vendor's training-data policy reviewed (e.g., default
  opt-outs for API tiers)

The personal-API-key route to commercial LLMs typically does NOT
meet healthcare or financial-data standards. Document which tier
is used.

### 6.7 User rights — export, access, deletion

Test each end-to-end:

- [ ] User can request a data export
- [ ] Export is complete (all regulated data, not just top-level
  records)
- [ ] Export is delivered securely (not emailed as attachment)
- [ ] User can delete account
- [ ] Deletion is complete and verifiable (no orphaned records)
- [ ] Deletion respects required retention (legal holds, financial
  reporting, regulatory minimums)

## Manual pass

### Manual §Access & authentication

- [ ] Unique user ID per person (no shared admin accounts)
- [ ] Auto-logoff configured (15 min for healthcare; longer for
  other contexts)
- [ ] Password policy meets baseline (length, no plain reuse of
  leaked passwords)
- [ ] 2FA available for all users; required for anyone with
  regulated-data access beyond their own

### Manual §Logging & monitoring

- [ ] Every regulated-data read logged: who, what, when, why
- [ ] Every regulated-data write logged: who, what changed, when
- [ ] Logs tamper-evident (write-once, hash-chained, or centralized
  to append-only store)
- [ ] Logs retained per applicable framework (HIPAA: ≥6 years;
  SOC 2: typically 1 year; GDPR: as long as needed for purpose)
- [ ] Log access itself is logged
- [ ] Anomaly alerts (mass export, unusual access patterns)

### Manual §Data minimization

- [ ] Data collected is documented and justified
- [ ] No "just in case" collection of sensitive fields
- [ ] Fields not used aren't stored
- [ ] Photos / videos / large artifacts stored only as long as
  needed

### Manual §Transmission

- [ ] TLS 1.2+ on all public endpoints
- [ ] HSTS set
- [ ] Internal service-to-service traffic encrypted or on private
  network
- [ ] No regulated data in URL paths/query strings (URL logging is
  common; this is an accidental leak path)

### Manual §Breach preparedness

- [ ] Written incident response plan exists
- [ ] Breach notification procedure documented (timeline varies:
  HIPAA 60 days, GDPR 72 hours, state laws differ)
- [ ] Contact list maintained (counsel, insurance, affected-party
  contact methods)
- [ ] Tabletop exercise performed in the last 12 months (or planned)

### Manual §Physical / operational

- [ ] Developer workstations: encrypted disks (FileVault, BitLocker,
  LUKS)
- [ ] Production credentials not on developer laptops (vaulted
  access + short-lived creds)
- [ ] Screen locks enforced
- [ ] Backup location and encryption documented

### Manual §Consent & privacy notices

- [ ] Privacy policy current and matches actual practice
- [ ] Terms of service reviewed with current data practices
- [ ] Consent obtained for processing regulated data at signup /
  onboarding
- [ ] Separate consent for AI processing (if used)
- [ ] Children's data — if any users under 13 / 16 / 18
  (jurisdiction-dependent), appropriate additional controls

## Findings synthesis

Severity skews higher in this domain:

- Anything exposing regulated data to unauthorized parties → **P0**
- Anything making exposure likely (weak access controls,
  unencrypted transit, regulated data in logs) → **P1**
- Missing process / documentation (no IR plan, no retention policy)
  → P1 or P2 depending on how central
- Minor policy gaps or non-critical improvements → P2 / P3

Group findings by the three control families (Administrative,
Physical, Technical) in the report.

**Always end the regulated-data report with this line:**
*"This audit identifies technical and procedural risks. It is not
legal counsel. Before making any regulatory compliance claim,
engage qualified counsel familiar with the applicable framework(s)."*

## Known gotchas (cross-project)

- Email / SMTP relays: confirm BAA / DPA tier (e.g., Google
  Workspace signs BAAs only for Enterprise tiers; personal /
  Standard does not).
- Mobile clients cache data locally — confirm local cache is
  encrypted (iOS keychain for sensitive bits; Android
  `EncryptedSharedPreferences` or equivalent).
- User-to-user messaging is a common spot where regulated-content
  details surface incidentally and are then logged / backed up
  indefinitely.
- Photos / uploaded media — these are regulated-grade for many
  data types; storage bucket permissions are critical.
- Free-form text fields (notes, comments) often contain regulated
  content even when the schema doesn't flag them.
- Engagement / client work artifacts (pentest reports, red-team
  outputs) need engagement-specific handling beyond regulatory
  defaults.
