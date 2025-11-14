DROP TABLE IF EXISTS qa_history;
DROP TABLE IF EXISTS dao_votes;
DROP TABLE IF EXISTS ref_proponents;
DROP TABLE IF EXISTS ref_messages;
DROP TABLE IF EXISTS ref_threads;
DROP TABLE IF EXISTS refs;
DROP TABLE IF EXISTS network_rpcs;
DROP TABLE IF EXISTS dao_members;
DROP TABLE IF EXISTS networks;
DROP TABLE IF EXISTS settings;

-- Settings (changed value to TEXT for longer content)
CREATE TABLE IF NOT EXISTS `settings` (
  `id` tinyint unsigned NOT NULL,
  `name` varchar(32) NOT NULL,
  `value` text NOT NULL,
  `active` tinyint NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Networks
CREATE TABLE IF NOT EXISTS `networks` (
  `id` tinyint unsigned NOT NULL,
  `name` varchar(32) NOT NULL,
  `symbol` varchar(8) NOT NULL,
  `url` varchar(256) NOT NULL,
  `discord_channel_id` varchar(64) DEFAULT NULL,
  `polkassembly_seed` varchar(512) DEFAULT NULL,
  `ss58_prefix` smallint unsigned DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_network_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- RPC endpoints
CREATE TABLE IF NOT EXISTS `network_rpcs` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `network_id` tinyint unsigned NOT NULL,
  `url` varchar(256) NOT NULL,
  `active` tinyint(1) DEFAULT '1',
  PRIMARY KEY (`id`),
  KEY `idx_rpc_network` (`network_id`),
  CONSTRAINT `fk_rpc_network` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- DAO members
CREATE TABLE IF NOT EXISTS `dao_members` (
  `address` varchar(128) NOT NULL,
  `discord` varchar(64) DEFAULT NULL,
  `is_admin` tinyint(1) DEFAULT '0',
  PRIMARY KEY (`address`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Proposals/Referenda with new columns for Polkassembly integration
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
  `submitted` bigint unsigned DEFAULT '0',
  `submitted_at` timestamp NULL DEFAULT NULL,
  `decision_start` bigint unsigned DEFAULT '0',
  `decision_end` bigint unsigned DEFAULT '0',
  `confirm_start` bigint unsigned DEFAULT '0',
  `confirm_end` bigint unsigned DEFAULT '0',
  `approved` tinyint(1) DEFAULT '0',
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
  `polkassembly_comment_id` varchar(64) DEFAULT NULL,
  `last_reply_check` timestamp NULL DEFAULT NULL,
  `finalized` tinyint(1) DEFAULT '0',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_proposal_network_ref` (`network_id`,`ref_id`),
  KEY `idx_proposal_status` (`status`),
  KEY `idx_proposal_track` (`track_id`),
  KEY `idx_proposal_submitter` (`submitter`),
  KEY `idx_finalized` (`finalized`),
  CONSTRAINT `fk_proposal_network` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Thread mapping table for Discord threads to referenda
CREATE TABLE IF NOT EXISTS `ref_threads` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `thread_id` varchar(64) NOT NULL,
  `ref_db_id` bigint unsigned NOT NULL,
  `network_id` tinyint unsigned NOT NULL,
  `ref_id` bigint unsigned NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_thread_id` (`thread_id`),
  KEY `idx_ref_db_id` (`ref_db_id`),
  KEY `idx_network_ref` (`network_id`, `ref_id`),
  CONSTRAINT `fk_thread_ref` FOREIGN KEY (`ref_db_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Messages between DAO and proponents with Polkassembly integration
CREATE TABLE IF NOT EXISTS `ref_messages` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `ref_id` bigint unsigned NOT NULL,
  `author` varchar(128) NOT NULL,
  `body` text NOT NULL,
  `internal` tinyint(1) DEFAULT '0',
  `polkassembly_user_id` int unsigned DEFAULT NULL,
  `polkassembly_username` varchar(128) DEFAULT NULL,
  `polkassembly_comment_id` varchar(64) DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_message_proposal` (`ref_id`),
  KEY `idx_message_author` (`author`),
  CONSTRAINT `fk_message_proposal` FOREIGN KEY (`ref_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Q&A history for the AI module
CREATE TABLE IF NOT EXISTS `qa_history` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `network_id` tinyint unsigned NOT NULL,
  `ref_id` int unsigned NOT NULL,
  `thread_id` varchar(64) NOT NULL,
  `user_id` varchar(64) NOT NULL,
  `question` text NOT NULL,
  `answer` text NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_qa_ref` (`network_id`,`ref_id`),
  KEY `idx_qa_thread` (`thread_id`),
  KEY `idx_qa_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Proposal participants
CREATE TABLE IF NOT EXISTS `ref_proponents` (
  `ref_id` bigint unsigned NOT NULL,
  `address` varchar(128) NOT NULL,
  `role` varchar(32) DEFAULT NULL COMMENT 'submitter, voter, delegator, etc',
  `active` tinyint DEFAULT '1',
  PRIMARY KEY (`ref_id`,`address`),
  KEY `idx_participant_address` (`address`),
  CONSTRAINT `fk_participant_proposal` FOREIGN KEY (`ref_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- DAO votes (for internal DAO voting)
CREATE TABLE IF NOT EXISTS `dao_votes` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `ref_id` bigint unsigned NOT NULL,
  `dao_member_id` varchar(128) NOT NULL,
  `choice` int NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_vote_proposal_voter` (`ref_id`,`dao_member_id`),
  KEY `idx_vote_dao_member` (`dao_member_id`),
  CONSTRAINT `fk_vote_dao_member` FOREIGN KEY (`dao_member_id`) REFERENCES `dao_members` (`address`),
  CONSTRAINT `fk_vote_proposal` FOREIGN KEY (`ref_id`) REFERENCES `refs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Insert initial settings with your actual values
INSERT INTO settings (id, name, value, active) VALUES
    (1, 'site_name', 'Opengov Communications Platform', 1),
    (2, 'discord_token', '', 1),
    (3, 'feedback_role_id', '', 1),
    (4, 'guild_id', '', 1),
    (5, 'indexer_workers', '10', 1),
    (6, 'indexer_interval_minutes', '60', 1),
    (7, 'polkassembly_intro', '', 1),
    (8, 'polkassembly_outro', '', 1);

-- Insert network data with Discord channel IDs
INSERT INTO networks (id, name, symbol, url, discord_channel_id) VALUES
    (1, 'Polkadot', 'DOT', 'https://polkadot.network', ''),
    (2, 'Kusama', 'KSM', 'https://kusama.network', '');

-- Insert RPC endpoints
INSERT INTO network_rpcs (network_id, url, active) VALUES
    (1, 'wss://polkadot.dotters.network/', 1),
    (2, 'wss://kusama.dotters.network/', 1);