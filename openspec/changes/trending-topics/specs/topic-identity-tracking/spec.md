## ADDED Requirements

### Requirement: Topic identity matching via Jaccard similarity
The system SHALL maintain a `topic_identity` table with columns: topic_id (TEXT PRIMARY KEY, UUID), canonical_label (TEXT), keywords (TEXT, JSON array including synonyms), first_seen (TEXT), last_seen (TEXT), peak_rank (INTEGER). When a new ranking is computed, each cluster's keyword+synonym set SHALL be compared against topic_identity entries seen in the last 48 hours using Jaccard similarity. If similarity exceeds 0.3, the cluster inherits the existing topic_id and the identity record is updated. Otherwise, a new UUID is assigned.

#### Scenario: Existing topic is matched
- **WHEN** a new cluster has Jaccard similarity > 0.3 with an existing topic_identity entry
- **THEN** the cluster uses the existing topic_id, and the identity's canonical_label, keywords, last_seen, and peak_rank are updated

#### Scenario: New topic is created
- **WHEN** a new cluster has Jaccard similarity <= 0.3 with all recent topic_identity entries
- **THEN** a new UUID is generated as topic_id, and a new topic_identity row is created with first_seen = now, last_seen = now, peak_rank = current rank

#### Scenario: Peak rank is tracked
- **WHEN** a matched topic's current rank is better (lower number) than its stored peak_rank
- **THEN** peak_rank is updated to the new rank

### Requirement: Topic identity retention
Topic identity records SHALL be retained for 7 days (based on last_seen). Records with last_seen older than 7 days SHALL be purged during the analysis cycle.

#### Scenario: Stale identity purged
- **WHEN** a topic_identity row has last_seen older than 7 days
- **THEN** the row is deleted during the purge cycle

#### Scenario: Active identity retained
- **WHEN** a topic_identity row has last_seen within the last 7 days
- **THEN** the row SHALL NOT be deleted
