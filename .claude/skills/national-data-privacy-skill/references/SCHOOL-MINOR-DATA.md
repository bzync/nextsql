# School and Minor Data Privacy

School platforms deserve a higher privacy baseline because they often combine identity, age, education records, parent/guardian information, attendance, grades, disciplinary information, financial data and other sensitive records.

## Default controls

- isolate schools/workspaces/tenants server-side;
- authorize by actual role + relationship + school scope, not UI visibility;
- separate learner, guardian, faculty and administrative permissions;
- avoid exposing full class/student datasets to roles that only need aggregates;
- require strong controls for report-card/permanent-record exports;
- audit high-impact edits and exports;
- preserve historical education records according to applicable school-record rules while applying privacy minimization to unrelated data;
- avoid real student data in development/demo environments;
- protect guardian/contact data from broad staff access;
- treat bulk import/export and integration endpoints as high-risk;
- require privacy review for biometrics, face recognition, AI profiling, behavior monitoring and location tracking.

## Consent caution

Do not assume parental/guardian consent is the universal basis for school processing. Determine the actual statutory, contractual, institutional and DPA basis for each purpose. Avoid bundling optional marketing/analytics consent into required educational processing.

## Historical records

Privacy-driven changes must not silently corrupt or rewrite historically valid academic records. Use controlled retention, restriction, pseudonymization or archival techniques appropriate to the legal/educational obligation.
