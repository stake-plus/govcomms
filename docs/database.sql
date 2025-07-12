-- Drop existing tables if needed (in correct order due to foreign keys)
DROP TABLE IF EXISTS dao_votes;
DROP TABLE IF EXISTS ref_subs;
DROP TABLE IF EXISTS ref_proponents;
DROP TABLE IF EXISTS ref_messages;
DROP TABLE IF EXISTS refs;
DROP TABLE IF EXISTS network_rpcs;
DROP TABLE IF EXISTS dao_members;
DROP TABLE IF EXISTS networks;
DROP TABLE IF EXISTS settings;

-- Settings
CREATE TABLE IF NOT EXISTS `settings` (
  `id` tinyint unsigned NOT NULL,
  `name` varchar(32) NOT NULL,
  `value` varchar(256) NOT NULL,
  `active` tinyint NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO settings (id, name, value, active) VALUES
    (1,'site_name', 'REEEEEEEEEE DAO', 1),
    (2,'site_url', 'https://reeeeeeeeee.io/', 1),
    (3,'site_logo', 'https://reeeeeeeeee.io/images/logo.png', 1),
    (4,'gc_url', 'https://gc.reeeeeeeeee.io/', 1),
    (5,'gcapi_url', 'https://api.gc.reeeeeeeeee.io/', 1),
    (6,'polkassembly_api', 'https://api.polkassembly.io/api/v1', 1),
    (7,'walletconnect_project_id', '', 1);

-- Networks
CREATE TABLE IF NOT EXISTS `networks` (
  `id` tinyint unsigned NOT NULL,
  `name` varchar(32) NOT NULL,
  `symbol` varchar(8) NOT NULL,
  `url` varchar(256) NOT NULL,
  `discord_channel_id` varchar(64) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_network_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- RPC endpoints
CREATE TABLE IF NOT EXISTS `network_rpcs` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `network_id` tinyint unsigned NOT NULL,
  `url` varchar(256) NOT NULL,
  `active` boolean DEFAULT true,
  PRIMARY KEY (`id`),
  KEY `idx_rpc_network` (`network_id`),
  CONSTRAINT `fk_rpc_network` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- DAO members (moved before refs to avoid foreign key issues)
CREATE TABLE IF NOT EXISTS `dao_members` (
  `address` varchar(128) NOT NULL,
  `discord` varchar(64) DEFAULT NULL,
  `is_admin` boolean DEFAULT false,
  PRIMARY KEY (`address`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Proposals/Referenda
CREATE TABLE IF NOT EXISTS `refs` (
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

-- Messages between DAO and proponents
CREATE TABLE IF NOT EXISTS `ref_messages` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `ref_id` bigint unsigned NOT NULL,
  `author` varchar(128) NOT NULL,
  `body` text NOT NULL,
  `internal` boolean DEFAULT false,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_message_proposal` (`ref_id`),
  KEY `idx_message_author` (`author`),
  CONSTRAINT `fk_message_proposal` FOREIGN KEY (`ref_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Proposal participants (anyone who interacted with the proposal)
CREATE TABLE IF NOT EXISTS `ref_proponents` (
  `ref_id` bigint unsigned NOT NULL,
  `address` varchar(128) NOT NULL,
  `role` varchar(32) DEFAULT NULL COMMENT 'submitter, voter, delegator, etc',
  `active` tinyint DEFAULT '1',
  PRIMARY KEY (`ref_id`, `address`),
  KEY `idx_participant_address` (`address`),
  CONSTRAINT `fk_participant_proposal` FOREIGN KEY (`ref_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Email subscriptions
CREATE TABLE IF NOT EXISTS `ref_subs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `message_id` bigint unsigned NOT NULL,
  `email` varchar(256) NOT NULL,
  `sent_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_subscription_message` (`message_id`),
  CONSTRAINT `fk_subscription_message` FOREIGN KEY (`message_id`) REFERENCES `ref_messages` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Votes (for internal DAO voting, not on-chain votes)
CREATE TABLE IF NOT EXISTS `dao_votes` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `ref_id` bigint unsigned NOT NULL,
  `dao_member_id` varchar(128) NOT NULL,
  `choice` int(2) NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_vote_proposal_voter` (`ref_id`, `dao_member_id`),
  KEY `idx_vote_dao_member` (`dao_member_id`),
  CONSTRAINT `fk_vote_proposal` FOREIGN KEY (`ref_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_vote_dao_member` FOREIGN KEY (`dao_member_id`) REFERENCES `dao_members` (`address`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Insert initial data
INSERT INTO networks (id, name, symbol, url, discord_channel_id) VALUES 
    (1, 'Polkadot', 'DOT', 'https://polkadot.network', '1293381448815870023'),
    (2, 'Kusama', 'KSM', 'https://kusama.network', '1293381492310937600');

INSERT INTO network_rpcs (network_id, url, active) VALUES
    (1, 'wss://polkadot.dotters.network/', 1),
    (2, 'wss://kusama.dotters.network/', 1);