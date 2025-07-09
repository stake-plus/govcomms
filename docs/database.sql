-- networks & RPCs
CREATE TABLE IF NOT EXISTS networks (
  id TINYINT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(32) UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS rpcs (
  id INT AUTO_INCREMENT PRIMARY KEY,
  network_id TINYINT NOT NULL,
  url VARCHAR(256) NOT NULL,
  active BOOLEAN DEFAULT TRUE,
  FOREIGN KEY (network_id) REFERENCES networks(id)
);

-- governance data
CREATE TABLE IF NOT EXISTS proposals (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  network_id TINYINT NOT NULL,
  ref_id BIGINT NOT NULL,
  submitter VARCHAR(64) NOT NULL,
  title VARCHAR(255),
  status VARCHAR(40),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uniq_ref (network_id, ref_id),
  FOREIGN KEY (network_id) REFERENCES networks(id)
);

CREATE TABLE IF NOT EXISTS messages (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  proposal_id BIGINT NOT NULL,
  author VARCHAR(64) NOT NULL,
  body TEXT NOT NULL,
  internal BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (proposal_id) REFERENCES proposals(id)
);

CREATE TABLE IF NOT EXISTS dao_members (
  address VARCHAR(64) PRIMARY KEY,
  discord VARCHAR(64)
);

CREATE TABLE IF NOT EXISTS votes (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  proposal_id BIGINT NOT NULL,
  voter_addr VARCHAR(64) NOT NULL,
  choice ENUM('aye','nay','abstain') NOT NULL,
  conviction SMALLINT DEFAULT 0,
  FOREIGN KEY (proposal_id) REFERENCES proposals(id),
  FOREIGN KEY (voter_addr) REFERENCES dao_members(address)
);

CREATE TABLE IF NOT EXISTS email_subscriptions (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  message_id BIGINT NOT NULL,
  email VARCHAR(256) NOT NULL,
  sent_at TIMESTAMP NULL,
  FOREIGN KEY (message_id) REFERENCES messages(id)
);

CREATE INDEX IF NOT EXISTS idx_messages_prop ON messages(proposal_id, created_at);
CREATE INDEX IF NOT EXISTS idx_votes_prop ON votes(proposal_id);
