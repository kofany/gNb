# gNb - IRC Nick Management Bot //

gNb is a Go-based IRC bot designed to manage and monitor nicknames on IRC networks. It allows users to automatically track and capture desired nicknames when they become available. Additionally, gNb provides functionalities for bot owners to interact with the bot through commands and supports BNC (bouncer) capabilities via SSH connections.

## Table of Contents

- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Running the Bot](#running-the-bot)
- [Commands](#commands)
- [BNC Usage](#bnc-usage)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Nick Monitoring and Capturing**: Automatically monitors specified nicknames and attempts to capture them when they become available.
- **Multi-Bot Management**: Supports managing multiple bots across different servers or vhosts.
- **Owner Commands**: Allows bot owners to execute commands such as joining channels, sending messages, and more.
- **BNC Functionality**: Provides BNC capabilities via SSH, allowing owners to connect and send raw IRC commands.
- **Configurable Logging**: Supports different logging levels and outputs to log files.
- **Dynamic Configuration**: Reads configurations from YAML and JSON files for flexibility.
- **Autojoin #literki after letter catch**: To diseble edit NICK callback in bot.go file. 
- **Deadlock Prevention**: Improved lock management to prevent system-wide hangs during multiple concurrent operations.

## Prerequisites

- **Go Programming Language**: Version 1.23 or higher.
- **Access to an IRC Server**: Ensure you have the necessary permissions to connect bots to your target IRC server.
- **Network Configuration**: If using custom vhosts, ensure they are properly configured on your network interface.

## Installation

1. **Clone the Repository**

   ```bash
   git clone https://github.com/kofany/gNb.git
   cd gNb
   ```

2. **Install Dependencies**

   ```bash
   go mod download
   ```

3. **Build the Project**

   ```bash
   go build -o gnb ./cmd/main.go
   ```

   This will create an executable named `gnb` in the project directory.

## Configuration

Before running the bot, you need to configure it properly.

1. **Configuration Files**
   The configuration files are located in the `configs` directory:

   - `config.yaml`: Main configuration file.
   - `owners.json`: List of bot owners who can execute commands.

2. **config.yaml Example**

   ```yaml
   global:
     log_level: debug  # Logging level: debug, info, warning, error
     ison_interval: 2  # Interval in seconds between ISON requests
     nick_api:
       url: 'https://i.got.al/words.php'
       max_word_length: 12
       timeout: 5  # Timeout for API requests in seconds
     max_nick_length: 14
     owner_command_prefixes:
       - "!"
       - "."
       - "@"
     reconnect_retries: 3
     reconnect_interval: 2
     mass_command_cooldown: 5

   bots:
     - server: irc.example.net
       port: 6667
       ssl: false
       vhost: 192.0.2.1  # Example IPv4
     - server: irc.example.net
       port: 6667
       ssl: false
       vhost: 2001:0db8::1  # Example IPv6

   channels:
     - "#examplechannel"
   ```

3. **owners.json Example**

   ```json
   {
     "owners": [
       "*!username@hostmask"
     ]
   }
   ```

   Replace `username@hostmask` with the appropriate ident and hostmask of the owners.

4. **data/nicks.json**
   List of nicknames to monitor and capture.

   ```json
   {
     "nicks": [
       "DesiredNick1",
       "DesiredNick2",
       "DesiredNick3"
     ]
   }
   ```

5. **Updating Configuration**
   Ensure you customize these files according to your needs. The bot will not run properly without correct configurations.

## Running the Bot

### Development Mode
To run the bot in the foreground for testing and development:

```bash
./gnb -dev
```

### Production Mode
To run the bot as a daemon in the background:

```bash
./gnb
```

Logs will be written to `bot.log` in the project directory.

### Stopping the Bot
To stop the bot, you can send a termination signal:

```bash
pkill -f gnb
```

Or, if running in development mode, use `Ctrl+C` in the terminal where the bot is running.

## Commands

Bot owners can control the bot using commands sent via private messages or in channels where the bot is present.

### Command Prefixes
Commands must start with one of the specified prefixes: `!`, `.`, or `@`.

### Available Commands

- **quit**: Instructs the bot to disconnect from the IRC server.  
  Usage: `!quit`

- **say**: Sends a message to a specified target (channel or user).  
  Usage: `!say <target> <message>`

- **join**: Makes the bot join a specified channel.  
  Usage: `!join <#channel>`

- **part**: Makes the bot leave a specified channel.  
  Usage: `!part <#channel>`

- **reconnect**: Reconnects the bot to the IRC server with a new nickname.  
  Usage: `!reconnect`

- **addnick**: Adds a nickname to the monitoring list.  
  Usage: `!addnick <nickname>`

- **delnick**: Removes a nickname from the monitoring list.  
  Usage: `!delnick <nickname>`

- **listnicks**: Lists all nicknames currently being monitored.  
  Usage: `!listnicks`

- **addowner**: Adds a new owner to the bot.  
  Usage: `!addowner <mask>`

- **delowner**: Removes an owner from the bot.  
  Usage: `!delowner <mask>`

- **listowners**: Lists all current owners of the bot.  
  Usage: `!listowners`

- **bnc**: Manages BNC sessions.  
  Usage: `!bnc <start|stop>`

### Notes

- **Mass Commands**: When executed in a channel, some commands like `join`, `part`, and `reconnect` will be executed by all bots simultaneously.
- **Cooldown**: Mass commands have a cooldown period defined by `mass_command_cooldown` in the configuration to prevent abuse.

## BNC Usage

The bot supports BNC functionality, allowing owners to connect via SSH and interact with the IRC network through the bot.

### Starting a BNC Session
Send the command: `!bnc start`  
The bot will provide connection details privately, including the SSH command to use.

### Connecting to the BNC
Use the provided SSH command, which will look similar to:

```bash
ssh -p <port> <bot_nick>@<vhost> <password>
```

- **port**: The port on which the BNC server is listening.
- **bot_nick**: The nickname of the bot you are connecting to.
- **vhost**: The virtual host or IP address specified in the bot's configuration.
- **password**: The one-time password provided by the bot.

### Interacting via BNC

- **Send Raw IRC Commands**: Type commands as you would in an IRC client.
- **Send Messages**: Use the format `. <target> <message>` to send messages to channels or users.

### Stopping a BNC Session
Send the command: `!bnc stop`  
This will terminate the BNC session for the bot.

## Contributing

Contributions are welcome! Please follow these steps:

1. **Fork the Repository**
2. **Create a Feature Branch**

   ```bash
   git checkout -b feature/YourFeature
   ```

3. **Commit Your Changes**

   ```bash
   git commit -m "Add your message"
   ```

4. **Push to Your Fork**

   ```bash
   git push origin feature/YourFeature
   ```

5. **Create a Pull Request**

## License

This project is licensed under the MIT License.

**Disclaimer**: Use this bot responsibly and ensure you comply with the IRC network's policies and guidelines. Unauthorized use or abuse may result in bans or other penalties from network administrators. 🙈

## Recent Updates

### Deadlock Prevention (v1.4.0)

We've implemented several improvements to prevent potential deadlocks in the system:

1. **Non-blocking Command Handling**: DCC commands like `listowners` now use timeouts and separate goroutines to prevent blocking the entire system.

2. **Improved Lock Management**: Replaced global lock with read-write locks (RWMutex) in critical components to allow concurrent read operations.

3. **Optimized ISON Processing**:
   - The NickManager now uses timeouts to prevent hanging on ISON requests
   - Better data copying to minimize lock times
   - Using non-blocking goroutines for nick monitoring operations

4. **Timeout Protection**: Added context-based timeouts to prevent indefinite waiting in critical operations.

These improvements ensure the system remains responsive even when multiple owners are connected via DCC and issuing commands simultaneously.
