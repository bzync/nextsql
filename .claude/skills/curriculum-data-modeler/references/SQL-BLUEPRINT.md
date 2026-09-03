# SQL Blueprint

This is engine-neutral conceptual SQL. Adapt types/indexes to the target DB.

```sql
CREATE TABLE education_agencies (
    id BIGINT PRIMARY KEY,
    code VARCHAR(32) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL
);

CREATE TABLE policy_issuances (
    id BIGINT PRIMARY KEY,
    agency_id BIGINT NOT NULL,
    canonical_code VARCHAR(100) NOT NULL UNIQUE,
    issuance_type VARCHAR(50) NOT NULL,
    issuance_number VARCHAR(50),
    series_year INT,
    title TEXT NOT NULL,
    issued_at DATE,
    effective_from VARCHAR(20),
    effective_until VARCHAR(20),
    status VARCHAR(50) NOT NULL,
    official_source_url TEXT,
    source_sha256 VARCHAR(64),
    verified_at TIMESTAMP,
    FOREIGN KEY (agency_id) REFERENCES education_agencies(id)
);

CREATE TABLE policy_relationships (
    id BIGINT PRIMARY KEY,
    source_policy_id BIGINT NOT NULL,
    target_policy_id BIGINT NOT NULL,
    relationship_type VARCHAR(50) NOT NULL,
    provision_scope TEXT,
    FOREIGN KEY (source_policy_id) REFERENCES policy_issuances(id),
    FOREIGN KEY (target_policy_id) REFERENCES policy_issuances(id)
);

CREATE TABLE curricula (
    id BIGINT PRIMARY KEY,
    agency_id BIGINT NOT NULL,
    code VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    FOREIGN KEY (agency_id) REFERENCES education_agencies(id)
);

CREATE TABLE curriculum_versions (
    id BIGINT PRIMARY KEY,
    curriculum_id BIGINT NOT NULL,
    version_code VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL,
    valid_from_school_year VARCHAR(20),
    valid_until_school_year VARCHAR(20),
    immutable_after_publish BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (curriculum_id, version_code),
    FOREIGN KEY (curriculum_id) REFERENCES curricula(id)
);

CREATE TABLE curriculum_policy_bases (
    curriculum_version_id BIGINT NOT NULL,
    policy_id BIGINT NOT NULL,
    relation VARCHAR(50) NOT NULL,
    PRIMARY KEY (curriculum_version_id, policy_id, relation),
    FOREIGN KEY (curriculum_version_id) REFERENCES curriculum_versions(id),
    FOREIGN KEY (policy_id) REFERENCES policy_issuances(id)
);

CREATE TABLE curriculum_subjects (
    id BIGINT PRIMARY KEY,
    curriculum_version_id BIGINT NOT NULL,
    grade_level_code VARCHAR(50),
    subject_code VARCHAR(100) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    sequence_no INT,
    UNIQUE (curriculum_version_id, grade_level_code, subject_code),
    FOREIGN KEY (curriculum_version_id) REFERENCES curriculum_versions(id)
);

CREATE TABLE competencies (
    id BIGINT PRIMARY KEY,
    curriculum_subject_id BIGINT NOT NULL,
    official_code VARCHAR(100),
    competency_text TEXT NOT NULL,
    domain_code VARCHAR(100),
    sequence_no INT,
    source_policy_id BIGINT,
    FOREIGN KEY (curriculum_subject_id) REFERENCES curriculum_subjects(id),
    FOREIGN KEY (source_policy_id) REFERENCES policy_issuances(id)
);

CREATE TABLE student_curriculum_assignments (
    id BIGINT PRIMARY KEY,
    student_id BIGINT NOT NULL,
    enrollment_id BIGINT NOT NULL,
    curriculum_version_id BIGINT NOT NULL,
    resolution_context_json TEXT,
    assigned_at TIMESTAMP NOT NULL,
    FOREIGN KEY (curriculum_version_id) REFERENCES curriculum_versions(id)
);
```

## Why separate assignment?

It freezes the resolved curriculum for a learner/cohort instead of repeatedly deriving it from today's settings.
