# Philippine DPA Compliance Map

## Baseline sources

Verify the current versions at the National Privacy Commission before relying on them operationally.

- Republic Act No. 10173 — Data Privacy Act of 2012.
- Implementing Rules and Regulations of RA 10173.
- NPC Circular 16-03 — Personal Data Breach Management.
- NPC Circular 2020-03 — Data Sharing Agreements; check later clarifications.
- NPC Circular 2022-04 — registration of personal data processing systems / DPO and related notifications; check later amendments/advisories.
- NPC Advisory 2021-01 — Data Subject Rights.
- NPC Advisory 2024-04 — application of DPA/IRR/NPC issuances to AI systems processing personal data.
- NPC Advisory 2025-02 — Privacy Engineering in Systems Life Cycle Processes.
- NPC Advisory 2026-01 — data scraping of publicly available personal data.
- NPC Advisory 2026-02 — clarification on personal data breach notification submission.

The list is a source map, not a claim that every item applies to every controller/processor.

## Engineering questions

For each processing activity record:

| Question | Evidence |
|---|---|
| What is the explicit purpose? | product requirement / policy / contract |
| What data is necessary? | field inventory |
| Who are the data subjects? | category + age/vulnerability |
| Who determines purpose/means? | PIC/PIP analysis |
| Who receives/accesses data? | role matrix + integrations |
| Where is it stored/transferred? | data-flow map |
| How long is it retained? | retention schedule |
| How can rights be exercised? | DSAR workflow |
| What is the risk? | PIA/risk register |
| How is it protected? | controls + tests |
| What happens on incident? | breach runbook |

## No compliance-by-checkbox

A privacy notice, checkbox or consent record does not cure unnecessary or incompatible processing. Design the actual system around transparency, legitimate purpose and proportionality, then implement the correct legal mechanism.
