-- Drop existing tables if needed
DROP TABLE IF EXISTS email_subscriptions;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS votes;
DROP TABLE IF EXISTS proposal_participants;
DROP TABLE IF EXISTS proposals;
DROP TABLE IF EXISTS dao_members;
DROP TABLE IF EXISTS tracks;
DROP TABLE IF EXISTS rpcs;
DROP TABLE IF EXISTS networks;

-- Networks table
CREATE TABLE networks (
    id TINYINT UNSIGNED PRIMARY KEY,
    name VARCHAR(32) UNIQUE NOT NULL,
    symbol VARCHAR(8) NOT NULL,
    url VARCHAR(256) NOT NULL
);

-- Tracks table
CREATE TABLE tracks (
    id SMALLINT UNSIGNED PRIMARY KEY,
    network_id TINYINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    max_deciding INT UNSIGNED,
    decision_deposit VARCHAR(64),
    prepare_period INT UNSIGNED,
    decision_period INT UNSIGNED,
    confirm_period INT UNSIGNED,
    min_enactment_period INT UNSIGNED,
    min_approval VARCHAR(32),
    min_support VARCHAR(32),
    INDEX idx_network (network_id),
    FOREIGN KEY (network_id) REFERENCES networks(id)
);

-- Proposals table (enhanced)
CREATE TABLE proposals (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    network_id TINYINT UNSIGNED NOT NULL,
    ref_id BIGINT UNSIGNED NOT NULL,
    submitter VARCHAR(64) NOT NULL,
    title VARCHAR(255),
    status VARCHAR(32),
    track_id SMALLINT UNSIGNED,
    origin VARCHAR(64),
    enactment VARCHAR(32),
    submitted BIGINT UNSIGNED,
    submitted_at TIMESTAMP NULL,
    decision_start BIGINT UNSIGNED,
    decision_end BIGINT UNSIGNED,
    confirm_start BIGINT UNSIGNED,
    confirm_end BIGINT UNSIGNED,
    approved BOOLEAN DEFAULT FALSE,
    support VARCHAR(64),
    approval VARCHAR(64),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_network_ref (network_id, ref_id),
    INDEX idx_track (track_id),
    FOREIGN KEY (network_id) REFERENCES networks(id),
    FOREIGN KEY (track_id) REFERENCES tracks(id)
);

-- Proposal participants table
CREATE TABLE proposal_participants (
    proposal_id BIGINT UNSIGNED NOT NULL,
    address VARCHAR(64) NOT NULL,
    PRIMARY KEY (proposal_id, address),
    FOREIGN KEY (proposal_id) REFERENCES proposals(id)
);

-- DAO members table
CREATE TABLE dao_members (
    address VARCHAR(64) PRIMARY KEY,
    discord VARCHAR(64)
);

-- Messages table
CREATE TABLE messages (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    proposal_id BIGINT UNSIGNED NOT NULL,
    author VARCHAR(64) NOT NULL,
    body TEXT NOT NULL,
    internal BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_proposal (proposal_id),
    FOREIGN KEY (proposal_id) REFERENCES proposals(id)
);

-- Votes table (enhanced)
CREATE TABLE votes (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    proposal_id BIGINT UNSIGNED NOT NULL,
    voter_addr VARCHAR(64) NOT NULL,
    choice VARCHAR(8) NOT NULL,
    conviction SMALLINT DEFAULT 0,
    balance VARCHAR(64),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_proposal (proposal_id),
    FOREIGN KEY (proposal_id) REFERENCES proposals(id)
);

-- Email subscriptions table
CREATE TABLE email_subscriptions (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    message_id BIGINT UNSIGNED NOT NULL,
    email VARCHAR(256) NOT NULL,
    sent_at TIMESTAMP NULL,
    FOREIGN KEY (message_id) REFERENCES messages(id)
);

-- RPCs table
CREATE TABLE rpcs (
    id INT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    network_id TINYINT UNSIGNED NOT NULL,
    url VARCHAR(256) NOT NULL,
    active BOOLEAN DEFAULT TRUE,
    FOREIGN KEY (network_id) REFERENCES networks(id)
);

-- Insert initial data
INSERT INTO networks (id, name, symbol, url) VALUES 
    (1, 'Polkadot', 'DOT', 'https://polkadot.network'),
    (2, 'Kusama', 'KSM', 'https://kusama.network');

INSERT INTO rpcs (id, network_id, url, active) VALUES
    (1, 1, 'wss://polkadot.dotters.network/', 1),
    (2, 2, 'wss://kusama.dotters.network/', 1);

-- Insert Polkadot tracks (OpenGov)
INSERT INTO tracks (id, network_id, name, max_deciding, decision_deposit, prepare_period, decision_period, confirm_period, min_enactment_period) VALUES
    (0, 1, 'Root', 1, '100000000000000', 1200, 201600, 14400, 14400),
    (1, 1, 'Whitelisted Caller', 100, '10000000000000', 300, 201600, 300, 300),
    (10, 1, 'Staking Admin', 10, '50000000000000', 1200, 201600, 1800, 300),
    (11, 1, 'Treasurer', 10, '10000000000000', 1200, 201600, 1800, 14400),
    (12, 1, 'Lease Admin', 10, '50000000000000', 1200, 201600, 1800, 300),
    (13, 1, 'Fellowship Admin', 10, '50000000000000', 1200, 201600, 1800, 300),
    (14, 1, 'General Admin', 10, '50000000000000', 1200, 201600, 1800, 300),
    (15, 1, 'Auction Admin', 10, '50000000000000', 1200, 201600, 1800, 300),
    (20, 1, 'Referendum Canceller', 1000, '100000000000000', 1200, 100800, 1800, 300),
    (21, 1, 'Referendum Killer', 1000, '250000000000000', 1200, 201600, 1800, 300),
    (30, 1, 'Small Tipper', 200, '10000000000000', 100, 100800, 100, 100),
    (31, 1, 'Big Tipper', 100, '100000000000000', 100, 100800, 600, 100),
    (32, 1, 'Small Spender', 50, '100000000000000', 2400, 201600, 7200, 14400),
    (33, 1, 'Medium Spender', 50, '2000000000000000', 2400, 201600, 14400, 14400),
    (34, 1, 'Big Spender', 50, '10000000000000000', 2400, 201600, 28800, 14400);