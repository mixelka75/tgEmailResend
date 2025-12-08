# Email to Telegram Bot

[English](#english) | [Русский](#русский)

---

## English

Telegram bot that automatically forwards email messages to supergroup topics.

### Features

- **Email Forwarding** — new emails appear instantly in Telegram
- **Auto-detect OTP Codes** — verification codes are highlighted and can be copied with one click
- **IMAP IDLE** — real-time notifications for new emails
- **Auto-detect IMAP Server** — no need to specify server for popular providers
- **Mailcow Integration** — create mailboxes directly from Telegram (optional)
- **Multi-account** — different emails in different topics of the same group

### Quick Start

#### Option 1: Build from source

```bash
git clone https://github.com/mixelka/emailresend.git
cd emailresend
make build
```

#### Option 2: Docker

```bash
git clone https://github.com/mixelka/emailresend.git
cd emailresend
cp .env.example .env
# Edit .env with your settings
make docker-run
```

### Configuration

```bash
cp .env.example .env
```

Edit `.env` file:

```env
# Required
TELEGRAM_BOT_TOKEN=123456:ABC-DEF...    # from @BotFather
ENCRYPTION_KEY=your-32-character-key!!  # exactly 32 characters

# Optional: Mailcow integration
MAILCOW_URL=https://mail.example.com
MAILCOW_API_KEY=your-api-key
MAILCOW_DOMAIN=example.com
```

Generate encryption key:
```bash
make generate-key
```

### Bot Commands

| Command | Description |
|---------|-------------|
| `/connect email password` | Connect email to topic |
| `/connect email password imap.server:993` | Connect with custom IMAP server |
| `/create username` | Create new mailbox (Mailcow only) |
| `/disconnect` | Disconnect email from topic |
| `/status` | Show all connections status |
| `/help` | Show help |

### Usage

1. Create a supergroup with topics enabled
2. Add the bot to the group
3. Make the bot an administrator (to delete messages with passwords)
4. In a topic, run:

```
/connect myemail@gmail.com app_password
```

The IMAP server is detected automatically. For custom servers:

```
/connect myemail@custom.com password imap.custom.com:993
```

### Supported Email Providers

IMAP server is auto-detected for:
- Gmail, Google Workspace
- Outlook, Hotmail, Live
- Yahoo Mail
- Yandex, Mail.ru
- iCloud
- Proton Mail (via Bridge)
- And others via MX records

### Requirements

- Go 1.22+ (for building)
- Docker (optional)
- Telegram supergroup with topics enabled

### Project Structure

```
emailresend/
├── cmd/bot/              # Entry point
├── internal/
│   ├── config/           # Configuration
│   ├── database/         # SQLite
│   ├── email/            # IMAP client
│   ├── mailcow/          # Mailcow API (optional)
│   ├── parser/           # Email parsing
│   ├── formatter/        # Telegram formatting
│   └── telegram/         # Telegram bot
├── pkg/models/           # Data models
└── data/                 # Database storage
```

### Security

- Passwords are encrypted with AES-256-GCM before storage
- Commands with passwords are deleted immediately after processing
- Only group administrators can manage email accounts

---

## Русский

Telegram бот для автоматической пересылки email сообщений в топики супергрупп.

### Возможности

- **Пересылка писем** — новые письма автоматически появляются в Telegram
- **Автодетект кодов** — коды подтверждения (OTP) выделяются и копируются в один клик
- **IMAP IDLE** — мгновенные уведомления о новых письмах
- **Автоопределение сервера** — не нужно указывать IMAP сервер для популярных провайдеров
- **Mailcow интеграция** — создание почтовых ящиков прямо из Telegram (опционально)
- **Мультиаккаунт** — разные почты в разных топиках одной группы

### Быстрый старт

#### Вариант 1: Сборка из исходников

```bash
git clone https://github.com/mixelka/emailresend.git
cd emailresend
make build
```

#### Вариант 2: Docker

```bash
git clone https://github.com/mixelka/emailresend.git
cd emailresend
cp .env.example .env
# Отредактируйте .env
make docker-run
```

### Настройка

```bash
cp .env.example .env
```

Заполните `.env`:

```env
# Обязательные
TELEGRAM_BOT_TOKEN=123456:ABC-DEF...    # от @BotFather
ENCRYPTION_KEY=your-32-character-key!!  # ровно 32 символа

# Опционально: интеграция с Mailcow
MAILCOW_URL=https://mail.example.com
MAILCOW_API_KEY=your-api-key
MAILCOW_DOMAIN=example.com
```

Сгенерировать ключ шифрования:
```bash
make generate-key
```

### Команды бота

| Команда | Описание |
|---------|----------|
| `/connect email password` | Подключить почту к топику |
| `/connect email password imap.server:993` | Подключить с указанием сервера |
| `/create username` | Создать новый ящик (только Mailcow) |
| `/disconnect` | Отключить почту от топика |
| `/status` | Статус всех подключений |
| `/help` | Справка |

### Использование

1. Создайте супергруппу с включёнными топиками
2. Добавьте бота в группу
3. Сделайте бота администратором (для удаления сообщений с паролями)
4. В нужном топике напишите:

```
/connect myemail@gmail.com app_password
```

IMAP сервер определится автоматически. Для нестандартных серверов:

```
/connect myemail@custom.com password imap.custom.com:993
```

### Поддерживаемые провайдеры

IMAP сервер определяется автоматически для:
- Gmail, Google Workspace
- Outlook, Hotmail, Live
- Yahoo Mail
- Yandex, Mail.ru
- iCloud
- Proton Mail (через Bridge)
- И другие через MX записи

### Требования

- Go 1.22+ (для сборки)
- Docker (опционально)
- Супергруппа Telegram с включёнными топиками

### Безопасность

- Пароли шифруются AES-256-GCM перед сохранением
- Команды с паролями удаляются сразу после получения
- Только администраторы группы могут управлять почтами

---

## License

MIT
