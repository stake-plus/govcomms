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

-- Networks
CREATE TABLE IF NOT EXISTS `networks` (
  `id` tinyint unsigned NOT NULL,
  `name` varchar(32) NOT NULL,
  `symbol` varchar(8) NOT NULL,
  `url` varchar(256) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_network_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- RPC endpoints
CREATE TABLE IF NOT EXISTS `rpcs` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `network_id` tinyint unsigned NOT NULL,
  `url` varchar(256) NOT NULL,
  `active` boolean DEFAULT true,
  PRIMARY KEY (`id`),
  KEY `idx_rpc_network` (`network_id`),
  CONSTRAINT `fk_rpc_network` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Proposals/Referenda
CREATE TABLE IF NOT EXISTS `proposals` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `network_id` tinyint unsigned NOT NULL,
  `ref_id` bigint unsigned NOT NULL,
  `submitter` varchar(128) NOT NULL,
  `title` varchar(255) DEFAULT NULL,
  `status` varchar(32) DEFAULT NULL,
  `track_id` smallint unsigned DEFAULT NULL,
  `origin` varchar(64) DEFAULT NULL,
  `enactment` varchar(32) DEFAULT NULL,
  `submitted` bigint unsigned DEFAULT 0,
  `submitted_at` timestamp NULL DEFAULT NULL,
  `decision_start` bigint unsigned DEFAULT 0,
  `decision_end` bigint unsigned DEFAULT 0,
  `confirm_start` bigint unsigned DEFAULT 0,
  `confirm_end` bigint unsigned DEFAULT 0,
  `approved` boolean DEFAULT false,
  `support` varchar(64) DEFAULT NULL,
  `approval` varchar(64) DEFAULT NULL,
  `ayes` varchar(64) DEFAULT NULL,
  `nays` varchar(64) DEFAULT NULL,
  `turnout` varchar(64) DEFAULT NULL,
  `electorate` varchar(64) DEFAULT NULL,
  `preimage_hash` varchar(128) DEFAULT NULL,
  `preimage_len` int unsigned DEFAULT NULL,
  `decision_deposit_who` varchar(128) DEFAULT NULL,
  `decision_deposit_amount` varchar(64) DEFAULT NULL,
  `submission_deposit_who` varchar(128) DEFAULT NULL,
  `submission_deposit_amount` varchar(64) DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_proposal_network_ref` (`network_id`, `ref_id`),
  KEY `idx_proposal_status` (`status`),
  KEY `idx_proposal_track` (`track_id`),
  KEY `idx_proposal_submitter` (`submitter`),
  CONSTRAINT `fk_proposal_network` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Proposal participants (anyone who interacted with the proposal)
CREATE TABLE IF NOT EXISTS `proposal_participants` (
  `proposal_id` bigint unsigned NOT NULL,
  `address` varchar(128) NOT NULL,
  `role` varchar(32) DEFAULT NULL COMMENT 'submitter, voter, delegator, etc',
  PRIMARY KEY (`proposal_id`, `address`),
  KEY `idx_participant_address` (`address`),
  CONSTRAINT `fk_participant_proposal` FOREIGN KEY (`proposal_id`) REFERENCES `proposals` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Messages between DAO and proponents
CREATE TABLE IF NOT EXISTS `messages` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `proposal_id` bigint unsigned NOT NULL,
  `author` varchar(128) NOT NULL,
  `body` text NOT NULL,
  `internal` boolean DEFAULT false,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_message_proposal` (`proposal_id`),
  KEY `idx_message_author` (`author`),
  CONSTRAINT `fk_message_proposal` FOREIGN KEY (`proposal_id`) REFERENCES `proposals` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- DAO members
CREATE TABLE IF NOT EXISTS `dao_members` (
  `address` varchar(128) NOT NULL,
  `discord` varchar(64) DEFAULT NULL,
  PRIMARY KEY (`address`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Votes (for internal DAO voting, not on-chain votes)
CREATE TABLE IF NOT EXISTS `votes` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `proposal_id` bigint unsigned NOT NULL,
  `voter_addr` varchar(128) NOT NULL,
  `choice` varchar(8) NOT NULL,
  `conviction` smallint DEFAULT 0,
  `balance` varchar(64) DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_vote_proposal_voter` (`proposal_id`, `voter_addr`),
  CONSTRAINT `fk_vote_proposal` FOREIGN KEY (`proposal_id`) REFERENCES `proposals` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Email subscriptions
CREATE TABLE IF NOT EXISTS `email_subscriptions` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `message_id` bigint unsigned NOT NULL,
  `email` varchar(256) NOT NULL,
  `sent_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_subscription_message` (`message_id`),
  CONSTRAINT `fk_subscription_message` FOREIGN KEY (`message_id`) REFERENCES `messages` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Tracks information
CREATE TABLE IF NOT EXISTS `tracks` (
  `id` smallint unsigned NOT NULL,
  `network_id` tinyint unsigned NOT NULL,
  `name` varchar(64) NOT NULL,
  `max_deciding` int unsigned DEFAULT NULL,
  `decision_deposit` varchar(64) DEFAULT NULL,
  `prepare_period` int unsigned DEFAULT NULL,
  `decision_period` int unsigned DEFAULT NULL,
  `confirm_period` int unsigned DEFAULT NULL,
  `min_enactment_period` int unsigned DEFAULT NULL,
  `min_approval` varchar(32) DEFAULT NULL,
  `min_support` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`id`, `network_id`),
  KEY `idx_track_network` (`network_id`),
  CONSTRAINT `fk_track_network` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Preimages
CREATE TABLE IF NOT EXISTS `preimages` (
  `hash` varchar(128) NOT NULL,
  `data` longtext DEFAULT NULL,
  `length` int unsigned DEFAULT NULL,
  `provider` varchar(128) DEFAULT NULL,
  `deposit` varchar(64) DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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