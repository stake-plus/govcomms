CREATE TABLE proposals (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  network ENUM('polkadot','kusama') NOT NULL,
  ref_id BIGINT NOT NULL,
  title VARCHAR(255),
  status VARCHAR(40),
  submitter VARCHAR(64),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY (network, ref_id)
);

CREATE TABLE addresses (
  addr VARCHAR(64) PRIMARY KEY,
  kind ENUM('single','multisig','proxy','pure_proxy') NOT NULL
);

CREATE TABLE address_links (
  parent_addr VARCHAR(64) NOT NULL,
  child_addr VARCHAR(64) NOT NULL,
  PRIMARY KEY (parent_addr, child_addr)
);

CREATE TABLE messages (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  proposal_id BIGINT NOT NULL,
  author VARCHAR(64) NOT NULL,
  body TEXT NOT NULL,
  internal BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (proposal_id) REFERENCES proposals(id)
);

CREATE TABLE email_subscriptions (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  message_id BIGINT NOT NULL,
  email VARCHAR(256) NOT NULL,
  FOREIGN KEY (message_id) REFERENCES messages(id)
);
