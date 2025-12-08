# Email to Telegram Bot

[English](#english) | [Русский](#русский)

---

## English

Telegram bot that forwards emails to supergroup topics in real-time.

### Features

- **Instant Notifications** — emails appear in Telegram within seconds (IMAP IDLE)
- **OTP Auto-detection** — verification codes are highlighted with copy button
- **Smart IMAP Detection** — no need to specify server for Gmail, Outlook, Yahoo, etc.
- **Mailcow Integration** — create mailboxes directly from Telegram (optional)
- **Multi-account** — each topic can have its own email account
- **Secure** — passwords encrypted with AES-256-GCM

---

### Quick Start

#### Step 1: Get Bot Token

1. Open [@BotFather](https://t.me/BotFather) in Telegram
2. Send `/newbot` and follow instructions
3. Copy the token

#### Step 2: Clone & Configure

```bash
git clone https://github.com/mixelka75/tgEmailResend.git
cd tgEmailResend
cp .env.example .env
```

Edit `.env`:
```env
TELEGRAM_BOT_TOKEN=123456:ABC-DEF...
ENCRYPTION_KEY=your-32-character-key!!
```

Generate encryption key:
```bash
make generate-key
```

#### Step 3: Run

**Option A: Direct**
```bash
make build
./emailbot
```

**Option B: Docker**
```bash
make docker-run
```

---

### Make Commands

| Command | Description |
|---------|-------------|
| `make help` | Show all available commands |
| `make build` | Build the binary |
| `make run` | Run without building |
| `make clean` | Remove binary and database |
| `make deps` | Download dependencies |
| `make docker` | Build Docker image |
| `make docker-run` | Start with Docker Compose |
| `make docker-stop` | Stop Docker Compose |
| `make generate-key` | Generate 32-char encryption key |
| `make test` | Run tests |
| `make lint` | Run linter |

---

### Setup Telegram Group

1. **Create Supergroup**
   - Create a new group in Telegram
   - Convert to supergroup (happens automatically when you add topics)

2. **Enable Topics**
   - Open group settings
   - Find "Topics" section
   - Enable topics

3. **Add Bot**
   - Add your bot to the group
   - Make it administrator (needed to delete messages with passwords)

4. **Connect Email**
   - Go to any topic
   - Send: `/connect your@email.com password`

---

### Bot Commands

| Command | Description |
|---------|-------------|
| `/connect email password` | Connect email to current topic |
| `/connect email password server:993` | Connect with custom IMAP server |
| `/create username` | Create new mailbox (Mailcow) |
| `/disconnect` | Disconnect email from topic |
| `/status` | Show all connections |
| `/help` | Show help |

---

### Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | Yes | — | Bot token from @BotFather |
| `ENCRYPTION_KEY` | Yes | — | 32-character encryption key |
| `DATABASE_PATH` | No | `./data/emailbot.db` | SQLite database path |
| `LOG_LEVEL` | No | `info` | debug, info, warn, error |
| `LOG_FORMAT` | No | `text` | text (colored) or json |
| `IMAP_IDLE_TIMEOUT` | No | `25m` | IMAP IDLE timeout |

#### Mailcow Integration (Optional)

| Variable | Description |
|----------|-------------|
| `MAILCOW_URL` | Mailcow server URL |
| `MAILCOW_API_KEY` | API key from Mailcow admin |
| `MAILCOW_DOMAIN` | Domain for new mailboxes |

---

### Supported Email Providers

Auto-detected IMAP servers:
- Gmail, Google Workspace
- Outlook, Hotmail, Live, Office365
- Yahoo Mail
- Yandex, Mail.ru
- iCloud
- Proton Mail (via Bridge)
- Zoho, FastMail, GMX
- Custom domains (via MX lookup)

---

### Gmail Setup

1. Enable 2FA in Google Account
2. Go to Security → App passwords
3. Create new app password
4. Use this password with `/connect`

### Yandex Setup

1. Go to Yandex ID → Security
2. Create app password for mail
3. Use this password with `/connect`

---

### Docker Compose

```yaml
services:
  emailbot:
    build: .
    restart: unless-stopped
    env_file:
      - .env
    volumes:
      - ./data:/app/data
```

Run: `make docker-run`
Stop: `make docker-stop`
Logs: `docker compose logs -f`

---

## Русский

Telegram бот для пересылки email в топики супергрупп в реальном времени.

### Возможности

- **Мгновенные уведомления** — письма появляются за секунды (IMAP IDLE)
- **Автодетект OTP** — коды подтверждения выделяются с кнопкой копирования
- **Умное определение IMAP** — не нужно указывать сервер для Gmail, Outlook, Yahoo
- **Mailcow интеграция** — создание ящиков прямо из Telegram (опционально)
- **Мультиаккаунт** — каждый топик может иметь свой email
- **Безопасность** — пароли шифруются AES-256-GCM

---

### Быстрый старт

#### Шаг 1: Получите токен бота

1. Откройте [@BotFather](https://t.me/BotFather) в Telegram
2. Отправьте `/newbot` и следуйте инструкциям
3. Скопируйте токен

#### Шаг 2: Клонируйте и настройте

```bash
git clone https://github.com/mixelka75/tgEmailResend.git
cd tgEmailResend
cp .env.example .env
```

Отредактируйте `.env`:
```env
TELEGRAM_BOT_TOKEN=123456:ABC-DEF...
ENCRYPTION_KEY=your-32-character-key!!
```

Сгенерируйте ключ шифрования:
```bash
make generate-key
```

#### Шаг 3: Запустите

**Вариант A: Напрямую**
```bash
make build
./emailbot
```

**Вариант B: Docker**
```bash
make docker-run
```

---

### Команды Make

| Команда | Описание |
|---------|----------|
| `make help` | Показать все команды |
| `make build` | Собрать бинарник |
| `make run` | Запустить без сборки |
| `make clean` | Удалить бинарник и БД |
| `make deps` | Скачать зависимости |
| `make docker` | Собрать Docker образ |
| `make docker-run` | Запустить через Docker Compose |
| `make docker-stop` | Остановить Docker Compose |
| `make generate-key` | Сгенерировать ключ шифрования |
| `make test` | Запустить тесты |
| `make lint` | Запустить линтер |

---

### Настройка группы Telegram

1. **Создайте супергруппу**
   - Создайте новую группу в Telegram
   - Она автоматически станет супергруппой при включении топиков

2. **Включите топики**
   - Откройте настройки группы
   - Найдите раздел "Темы" (Topics)
   - Включите топики

3. **Добавьте бота**
   - Добавьте бота в группу
   - Сделайте его администратором (нужно для удаления сообщений с паролями)

4. **Подключите почту**
   - Перейдите в любой топик
   - Отправьте: `/connect ваша@почта.com пароль`

---

### Команды бота

| Команда | Описание |
|---------|----------|
| `/connect email password` | Подключить почту к топику |
| `/connect email password server:993` | С указанием IMAP сервера |
| `/create username` | Создать ящик (Mailcow) |
| `/disconnect` | Отключить почту |
| `/status` | Статус подключений |
| `/help` | Справка |

---

### Конфигурация

| Переменная | Обязательно | По умолчанию | Описание |
|------------|-------------|--------------|----------|
| `TELEGRAM_BOT_TOKEN` | Да | — | Токен от @BotFather |
| `ENCRYPTION_KEY` | Да | — | Ключ шифрования (32 символа) |
| `DATABASE_PATH` | Нет | `./data/emailbot.db` | Путь к SQLite |
| `LOG_LEVEL` | Нет | `info` | debug, info, warn, error |
| `LOG_FORMAT` | Нет | `text` | text (цветной) или json |
| `IMAP_IDLE_TIMEOUT` | Нет | `25m` | Таймаут IMAP IDLE |

#### Интеграция Mailcow (опционально)

| Переменная | Описание |
|------------|----------|
| `MAILCOW_URL` | URL сервера Mailcow |
| `MAILCOW_API_KEY` | API ключ из админки Mailcow |
| `MAILCOW_DOMAIN` | Домен для новых ящиков |

---

### Поддерживаемые провайдеры

Автоопределение IMAP для:
- Gmail, Google Workspace
- Outlook, Hotmail, Live, Office365
- Yahoo Mail
- Yandex, Mail.ru
- iCloud
- Proton Mail (через Bridge)
- Zoho, FastMail, GMX
- Свои домены (через MX lookup)

---

### Настройка Gmail

1. Включите 2FA в аккаунте Google
2. Перейдите в Безопасность → Пароли приложений
3. Создайте новый пароль приложения
4. Используйте этот пароль в `/connect`

### Настройка Яндекс

1. Откройте Яндекс ID → Безопасность
2. Создайте пароль приложения для почты
3. Используйте этот пароль в `/connect`

---

### Docker Compose

```yaml
services:
  emailbot:
    build: .
    restart: unless-stopped
    env_file:
      - .env
    volumes:
      - ./data:/app/data
```

Запуск: `make docker-run`
Остановка: `make docker-stop`
Логи: `docker compose logs -f`

---

## License

MIT
