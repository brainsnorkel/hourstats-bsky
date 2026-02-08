## MODIFIED Requirements

### Requirement: Profile Presence
The system SHALL ensure the yearly trend chart remains highly visible to profile visitors by pinning it to the top of the account's feed AND persisting its post identifiers for use by daily reply cycles.

#### Scenario: Automatic Profile Pinning
- **WHEN** the yearly sentiment chart is successfully posted to Bluesky
- **THEN** the system MUST immediately send a profile update request to the AT Protocol to pin the new post to the user's profile.

#### Scenario: Persist Yearly Post Identifiers
- **WHEN** the yearly sentiment chart is successfully posted to Bluesky
- **THEN** the system MUST store the post's URI under the key `yearly_post_uri` in the key-value table
- **AND** store the post's CID under the key `yearly_post_cid` in the key-value table
- **AND** these values MUST be available for retrieval by subsequent daily cycles
