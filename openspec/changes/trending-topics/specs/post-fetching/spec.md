## MODIFIED Requirements

### Requirement: Jetstream OnPost callback processes posts
The Jetstream OnPost callback SHALL, for each post event: increment the firehose counter, filter for non-empty text and English language, construct a Post struct, and insert into the post_buffer. Additionally, for root posts only (rec.Reply == nil), the callback SHALL tokenize the post text using the topic tokenizer and insert the cleaned tokens into the topic_tokens table. The tokenization step SHALL only execute when TRENDING_ENABLED is true. Tokenization failures SHALL be logged but SHALL NOT prevent the post from being inserted into post_buffer.

#### Scenario: Root post is both buffered and tokenized
- **WHEN** a root post passes language filtering and TRENDING_ENABLED is true
- **THEN** the post is inserted into post_buffer AND topic_tokens

#### Scenario: Reply post is buffered but not tokenized
- **WHEN** a reply post passes language filtering
- **THEN** the post is inserted into post_buffer only; no topic_tokens row is created

#### Scenario: Tokenization failure does not block post insertion
- **WHEN** tokenization or topic_tokens insertion fails for a root post
- **THEN** the error is logged and the post is still inserted into post_buffer

#### Scenario: Trending disabled skips tokenization
- **WHEN** TRENDING_ENABLED is false and a root post is received
- **THEN** no tokenization or topic_tokens insertion occurs
