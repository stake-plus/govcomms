# GCBot - GovComms Discord Bot

## Overview

GCBot is the Discord integration component of the GovComms platform. It manages the feedback command system, relays messages between the web platform and Discord, and handles the initial Polkassembly posting. The bot ensures seamless communication flow between referendum proponents and DAO members.

## Features

- **Feedback Command**: `!feedback` command for DAO members to submit feedback
- **Message Relay**: Automatically posts web platform messages to Discord threads
- **Thread Management**: Tracks and manages referendum discussion threads
- **Polkassembly Integration**: Posts first feedback to Polkassembly
- **Multi-Network Support**: Handles both Polkadot and Kusama networks
- **Rate Limiting**: Prevents spam and abuse

## Architecture

### Components

1. **Bot Core**: Discord.js wrapper managing connection and events
2. **Feedback Handler**: Processes !feedback commands
3. **Message Monitor**: Polls for new messages from the API
4. **Thread Manager**: Maps Discord threads to referenda
5. **Network Manager**: Handles multi-network configuration
6. **Polkassembly Service**: Posts feedback to Polkassembly

### Message Flow

1. DAO member uses `!feedback` in referendum thread
2. Bot validates permissions and thread context
3. Message stored in database via API
4. If first message, posted to Polkassembly
5. Proponent receives notification on web platform
6. Proponent responds via web interface
7. Bot posts response back to Discord thread

## Commands

### !feedback

Submit feedback for a referendum.

**Usage:**
    !feedback Your detailed feedback message here

**Requirements:**
- Must have feedback role
- Must be used in a referendum thread
- Message must be 10-5000 characters
- Rate limited to once per 30 seconds

**Example:**
    !feedback The treasury amount seems high for the deliverables. 
    Could you provide a breakdown of how the funds will be allocated?

## Configuration

### Environment Variables

    # Database
    MYSQL_DSN=user:pass@tcp(host:port)/database

    # Cache
    REDIS_URL=redis://host:port/db

    # Discord
    DISCORD_TOKEN=your-bot-token
    FEEDBACK_ROLE_ID=role-id-for-feedback
    GUILD_ID=your-discord-guild-id

    # Polkassembly (optional)
    POLKASSEMBLY_SEED="twelve word mnemonic seed phrase"
    # Or network-specific:
    POLKASSEMBLY_POLKADOT_SEED="polkadot specific seed"
    POLKASSEMBLY_KUSAMA_SEED="kusama specific seed"

### Database Configuration

The bot reads configuration from the database:
- Network Discord channels
- Polkassembly API endpoints
- Frontend URLs for links

## Discord Setup

### Bot Permissions

Required Discord permissions:
- Read Messages
- Send Messages
- Embed Links
- Read Message History
- View Channels
- Manage Threads

### Bot Intents

Required gateway intents:
- Guild Messages
- Message Content
- Guilds

### Role Setup

1. Create a feedback role in Discord
2. Assign role to authorized DAO members
3. Set role ID in environment config

### Channel Setup

1. Create a channel for each network (Polkadot/Kusama)
2. Configure channel IDs in database
3. Bot will monitor threads in these channels

## Thread Detection

The bot automatically detects referendum threads by name patterns:
- `#123: Title`
- `123 - Title`
- `[123] Title`
- `123 Title`

Threads must be in configured network channels.

## Message Formatting

### Feedback Submission Response

    Feedback Submitted
    Your feedback for Polkadot/123 has been submitted.

    Continue Discussion
    Click here to continue the conversation

    ✅ Successfully posted to Polkassembly!

### Relayed Messages

Messages from the web platform are displayed as embeds:

    Message from 5Grw...S0zH
    This is the message content from the proponent.

    Continue Discussion
    Click here

    Via GovComms | Polkadot #123

## Polkassembly Integration

### First Message Posting

When the first feedback is submitted:

1. Bot formats message with intro/outro
2. Creates Polkassembly-compatible markdown
3. Signs transaction with configured account
4. Posts comment to referendum
5. Includes link back to GovComms

### Message Format

    ## 🏛️ REEEEEEEEEE DAO Feedback

    The **REEEEEEEEEE DAO** is a decentralized collective...

    ### 📋 Community Feedback

    > Original feedback message here

    ---

    ### 💬 Continue the Discussion

    We welcome proponents to engage directly...

    👉 **[Continue discussion with the DAO](https://govcomms.io/polkadot/123)**

## Monitoring

### Service Status

    sudo systemctl status gcbot

### Logs

View real-time logs:

    sudo journalctl -u gcbot -f

### Key Log Messages

Successful startup:

    Discord bot logged in as GovComms#1234
    Thread synchronization complete. Synced 42 threads
    Starting Discord message monitor

Feedback processing:

    Feedback command received from User#5678 in channel 123456
    Processing feedback for Polkadot ref #123
    Feedback submitted for polkadot/123: 150 chars
    Successfully posted to Polkassembly for Polkadot ref #123

Message relay:

    Checking for new messages...
    Posting message 789 to thread 987654
    Message posted successfully

## Error Handling

### Common Errors

1. **Rate Limit Exceeded**
   - User message: "Please wait X minutes and Y seconds"
   - Log: Rate limit hit for user

2. **Invalid Thread**
   - User message: "This command must be used in a referendum thread"
   - Ensure thread name matches pattern

3. **No Permission**
   - User message: "You don't have permission"
   - Check user has feedback role

4. **Polkassembly Failed**
   - Warning sent to Discord
   - Message still saved in database
   - Manual intervention may be needed

### Recovery Procedures

1. **Bot Disconnected**
   - Auto-reconnects with exponential backoff
   - Check Discord token validity
   - Verify network connectivity

2. **Database Connection Lost**
   - Reconnection attempted automatically
   - Check MySQL service status
   - Verify credentials

3. **Thread Sync Issues**
   - Run manual sync on startup
   - Check channel permissions
   - Verify thread naming

## Development

### Testing Commands

Test feedback processing:

    !feedback Test message for development

### Debug Mode

Enable debug logging:

    LOG_LEVEL=debug ./gcbot

### Local Development

1. Create test Discord server
2. Set up test channels and roles
3. Use development database
4. Point to local API instance

## Best Practices

1. **Thread Management**
   - Keep thread names consistent
   - Archive old threads regularly
   - Monitor thread limits

2. **Role Management**
   - Regularly audit feedback role members
   - Document role assignment process
   - Use role hierarchies properly

3. **Message Handling**
   - Monitor message queue size
   - Handle long messages gracefully
   - Validate markdown formatting

4. **Security**
   - Rotate bot token periodically
   - Limit bot permissions to minimum needed
   - Monitor for suspicious activity

## Troubleshooting Guide

### Bot Won't Start

1. Check Discord token is valid
2. Verify database connection
3. Ensure Redis is running
4. Check file permissions

### Commands Not Working

1. Verify bot has message content intent
2. Check command prefix is correct
3. Ensure user has required role
4. Verify thread is detected correctly

### Messages Not Relaying

1. Check message monitor is running
2. Verify API connectivity
3. Check thread mapping in database
4. Review error logs

### Polkassembly Issues

1. Verify seed phrase is correct
2. Check network configuration
3. Test Polkassembly API directly
4. Monitor rate limits