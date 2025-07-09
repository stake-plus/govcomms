-- ──────────────────────────────────────────────────────────────────────────
-- GovComms schema (DROP + CREATE so repeated applies stay in sync)
-- ──────────────────────────────────────────────────────────────────────────

-- drop order: children → parents (due to FK constraints)
DROP TABLE IF EXISTS email_subscriptions;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS votes;
DROP TABLE IF EXISTS proposal_participants;
DROP TABLE IF EXISTS proposals;
DROP TABLE IF EXISTS dao_members;
DROP TABLE IF EXISTS rpcs;
DROP TABLE IF EXISTS networks;

-- ──────────────────────────────────────────────────────────────────────────
-- Core networks / RPC endpoints
-- ──────────────────────────────────────────────────────────────────────────
CREATE TABLE networks (
  id       TINYINT      AUTO_INCREMENT PRIMARY KEY,
  name     VARCHAR(32)  NOT NULL,
  symbol   VARCHAR(8)   NOT NULL,
  url      VARCHAR(256) NOT NULL,
  UNIQUE KEY uniq_network (name)
);

CREATE TABLE rpcs (
  id         INT          AUTO_INCREMENT PRIMARY KEY,
  network_id TINYINT      NOT NULL,
  url        VARCHAR(256) NOT NULL,
  active     BOOLEAN      DEFAULT TRUE,
  FOREIGN KEY (network_id) REFERENCES networks(id)
);

-- ──────────────────────────────────────────────────────────────────────────
-- Governance
-- ──────────────────────────────────────────────────────────────────────────
CREATE TABLE proposals (
  id          BIGINT        AUTO_INCREMENT PRIMARY KEY,
  network_id  TINYINT       NOT NULL,
  ref_id      BIGINT        NOT NULL,
  submitter   VARCHAR(64)   NOT NULL,
  title       VARCHAR(255),
  status      VARCHAR(40),
  end_block   BIGINT,
  created_at  TIMESTAMP     DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uniq_ref (network_id, ref_id),
  FOREIGN KEY (network_id) REFERENCES networks(id)
);

CREATE TABLE dao_members (
  address VARCHAR(64) PRIMARY KEY,
  discord VARCHAR(64)
);

CREATE TABLE proposal_participants (
  proposal_id BIGINT      NOT NULL,
  address     VARCHAR(64) NOT NULL,
  PRIMARY KEY (proposal_id, address),
  FOREIGN KEY (proposal_id) REFERENCES proposals(id),
  FOREIGN KEY (address)    REFERENCES dao_members(address)
);

CREATE TABLE messages (
  id          BIGINT        AUTO_INCREMENT PRIMARY KEY,
  proposal_id BIGINT        NOT NULL,
  author      VARCHAR(64)   NOT NULL,
  body        TEXT          NOT NULL,
  internal    BOOLEAN       DEFAULT FALSE,
  created_at  TIMESTAMP     DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (proposal_id) REFERENCES proposals(id)
);

CREATE TABLE votes (
  id          BIGINT      AUTO_INCREMENT PRIMARY KEY,
  proposal_id BIGINT      NOT NULL,
  voter_addr  VARCHAR(64) NOT NULL,
  choice      ENUM('aye','nay','abstain') NOT NULL,
  conviction  SMALLINT    DEFAULT 0,
  FOREIGN KEY (proposal_id) REFERENCES proposals(id),
  FOREIGN KEY (voter_addr) REFERENCES dao_members(address)
);

CREATE TABLE email_subscriptions (
  id         BIGINT       AUTO_INCREMENT PRIMARY KEY,
  message_id BIGINT       NOT NULL,
  email      VARCHAR(256) NOT NULL,
  sent_at    TIMESTAMP,
  FOREIGN KEY (message_id) REFERENCES messages(id)
);

CREATE INDEX idx_messages_prop ON messages(proposal_id, created_at);
CREATE INDEX idx_votes_prop    ON votes(proposal_id);
