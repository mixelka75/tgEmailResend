# Email → Telegram

[Русский](#русский) | [English](#english)

---

## Русский

Бот пересылает письма из почтовых ящиков в топики Telegram-супергруппы. Каждый топик — отдельный ящик.

### Возможности

- **Мгновенно** — новые письма приходят за секунды через IMAP IDLE; если сервер не умеет IDLE, бот опрашивает ящик (по умолчанию раз в минуту).
- **Красивые письма** — письма отправляются [rich-сообщениями](https://core.telegram.org/bots/api#rich-messages) (Bot API 10.1+): заголовки, списки, ссылки, жирный/курсив, цитаты переписки сворачиваются, длинные письма прячутся под «Читать дальше». Дата показывается в часовом поясе каждого читателя. Если rich-сообщение не проходит, бот автоматически отправляет обычное HTML-сообщение, а если не проходит и оно — простой текст.
- **Коды подтверждения** — коды из писем («Код подтверждения: 482913», «Your code is 1234», «123 456») находятся автоматически, кнопка копирует код в буфер обмена одним нажатием.
- **Кнопки** — «Прочитано» ставит отметку в самом ящике, «Удалить» (с подтверждением) переносит письмо в Корзину. Нажимать кнопки могут только администраторы группы.
- **Не теряет письма** — если Telegram недоступен, письмо будет отправлено позже; при обрыве связи с почтой бот переподключается сам и пишет в топик, только если проблема затянулась.
- **Автоопределение IMAP** — Gmail, Яндекс, Mail.ru, iCloud и др.; для своих доменов — по MX-записям (Google Workspace, Яндекс 360, Zoho, Fastmail…).
- **Mailcow** — создание ящиков прямо из Telegram командой `/create` (опционально).
- **Безопасность** — пароли хранятся зашифрованными (AES-256-GCM), сообщения с паролями сразу удаляются из чата, письма в ящике не помечаются прочитанными при пересылке.

### Быстрый старт

1. Создайте бота у [@BotFather](https://t.me/BotFather) (`/newbot`) и скопируйте токен.
2. Склонируйте и настройте:
   ```bash
   git clone https://github.com/mixelka75/tgEmailResend.git
   cd tgEmailResend
   cp .env.example .env
   make generate-key   # вставьте результат в ENCRYPTION_KEY
   ```
   В `.env` обязательно заполните `TELEGRAM_BOT_TOKEN` и `ENCRYPTION_KEY` (ровно 32 символа).
3. Запустите — через Docker:
   ```bash
   make docker-run
   docker compose logs -f
   ```
   или напрямую (нужны Go 1.24+ и gcc для SQLite):
   ```bash
   make build && ./emailbot
   ```

> ⚠️ Не меняйте `ENCRYPTION_KEY` после подключения ящиков: сохранённые пароли станет невозможно расшифровать, и ящики придётся подключить заново (бот сообщит об этом в топике).

### Настройка группы

1. Создайте группу и включите **Темы** (Настройки группы → Темы) — группа станет супергруппой с топиками.
2. Добавьте бота и сделайте его **администратором** с правами:
   - **Удаление сообщений** — чтобы сразу удалять команды с паролями и письма по кнопке «Удалить»;
   - **Управление темами** не нужно.
3. Откройте нужный топик и отправьте `/connect почта пароль`.

### Команды

| Команда | Что делает |
|---------|------------|
| `/connect email пароль` | Подключить ящик к текущему топику (сервер определяется сам) |
| `/connect email пароль imap.server.com:993` | То же с явным IMAP-сервером (порт 993 — TLS, 143 — STARTTLS) |
| `/create имя [пароль] [Имя Фамилия]` | Создать ящик в Mailcow и подключить его к топику |
| `/disconnect` | Отключить ящик от топика |
| `/status` | Список ящиков группы и их состояние |
| `/help` | Справка |

Управлять ящиками и нажимать кнопки под письмами могут только администраторы группы (включая анонимных).

### Почтовые сервисы

| Сервис | Что нужно |
|--------|-----------|
| Gmail / Google Workspace | Включить 2FA → [Пароли приложений](https://myaccount.google.com/apppasswords), использовать пароль приложения |
| Яндекс / Яндекс 360 | Яндекс ID → Безопасность → Пароли приложений → «Почта»; в настройках почты разрешить доступ по IMAP |
| Mail.ru | Настройки → Безопасность → Пароли для внешних приложений |
| iCloud | [Пароль приложения](https://appleid.apple.com) |
| Proton Mail | Только через [Proton Mail Bridge](https://proton.me/mail/bridge), сервер `127.0.0.1:1143` (бот и Bridge должны быть на одной машине) |
| Outlook / Hotmail / Microsoft 365 | ❌ Microsoft отключила вход по паролю в IMAP (нужен OAuth2), такие ящики не подключатся |
| Свой сервер | Любой IMAP-сервер с TLS (993) или STARTTLS (143) |

### Mailcow (опционально)

Команда `/create` создаёт ящик на вашем [Mailcow](https://mailcow.email) и сразу подключает его к топику.

1. В Mailcow: **Система → Конфигурация → Доступ → Изменить администратора → API**.
2. Создайте ключ **Read-Write** и добавьте IP сервера бота в «Разрешить API с IP» (или включите «Пропускать проверку IP»).
3. Заполните в `.env`:
   ```env
   MAILCOW_URL=https://mail.example.com
   MAILCOW_API_KEY=...
   MAILCOW_DOMAIN=example.com
   ```

Новые ящики получают квоту по умолчанию домена (или `MAILCOW_MAILBOX_QUOTA_MB`), доступ по IMAP и остальные настройки по умолчанию сервера. Если пароль не указан, бот сгенерирует надёжный, подходящий под политику паролей Mailcow. При старте бот проверяет ключ и домен и пишет результат в лог.

> Mailcow передаёт неудачные попытки входа по API в fail2ban: при неверном ключе или IP не повторяйте `/create` много раз подряд — IP бота могут заблокировать.

### Настройки

| Переменная | Обязательно | По умолчанию | Описание |
|------------|-------------|--------------|----------|
| `TELEGRAM_BOT_TOKEN` | да | — | Токен от @BotFather |
| `ENCRYPTION_KEY` | да | — | Ключ шифрования паролей, ровно 32 символа (`make generate-key`) |
| `TELEGRAM_RICH_MESSAGES` | нет | `true` | Rich-сообщения для писем; `false` — обычные HTML-сообщения |
| `TELEGRAM_API_URL` | нет | — | Свой [Bot API сервер](https://github.com/tdlib/telegram-bot-api), например `http://localhost:8081` |
| `DATABASE_PATH` | нет | `./data/emailbot.db` | Путь к базе SQLite |
| `EMAIL_POLL_INTERVAL` | нет | `1m` | Максимум между проверками ящика (опрос, если нет IDLE), от 10s |
| `IMAP_IDLE_TIMEOUT` | нет | `25m` | Перезапуск IDLE (1m–29m) |
| `IMAP_DIAL_TIMEOUT` | нет | `30s` | Таймаут подключения к IMAP |
| `MAILCOW_URL` | нет | — | Адрес Mailcow |
| `MAILCOW_API_KEY` | нет | — | API-ключ Read-Write |
| `MAILCOW_DOMAIN` | нет | — | Домен для новых ящиков |
| `MAILCOW_IMAP_SERVER` | нет | хост из `MAILCOW_URL`:993 | IMAP-сервер новых ящиков, если он отличается от веб-адреса |
| `MAILCOW_MAILBOX_QUOTA_MB` | нет | квота домена | Квота новых ящиков в МБ |
| `LOG_LEVEL` | нет | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | нет | `text` | `text` (цветной) или `json` |

### Как это работает

- При подключении бот запоминает последнее письмо в ящике: **старые письма не пересылаются**, только новые.
- Письма в ящике **не помечаются прочитанными** при пересылке — только кнопкой «Прочитано».
- «Удалить» переносит письмо в Корзину ящика (если её нет — помечает удалённым только это письмо, не трогая остальные). Сообщение в Telegram удаляется, если ему меньше 48 часов (ограничение Telegram), иначе у него пропадают кнопки.
- Если топик удалили, письма приходят в «Общий» с подсказкой переподключить почту.
- Если бота удалили из группы или запретили ему писать, такие письма пропускаются (см. лог).
- Из письма скачиваются только первые 2 МБ: текст всегда помещается, большие вложения не грузят память.
- Если сервер сменил нумерацию писем (UIDVALIDITY), бот начинает с текущего письма, а не пересылает ящик заново.

### Решение проблем

| Симптом | Что проверить |
|---------|---------------|
| `invalid TELEGRAM_BOT_TOKEN` | Токен скопирован целиком |
| `ENCRYPTION_KEY must be exactly 32 bytes` | Длина ключа — `make generate-key` |
| `Ошибка подключения … AUTHENTICATIONFAILED` | Нужен пароль приложения, IMAP включён в настройках почты |
| «Не удалось удалить сообщение с паролем» | У бота нет права «Удаление сообщений» |
| Письма не приходят, в `/status` 🟡 | Сервер недоступен, бот переподключается сам; подробности в логе |
| Ошибка `Conflict` в логе | Этот же токен используется другим запущенным ботом |

### Разработка

```bash
make test    # тесты, включая IMAP-сервер в памяти и фейковый Bot API
make lint
```

---

## English

A Telegram bot that forwards emails into topics of a supergroup, one mailbox per topic.

### Features

- **Instant** — IMAP IDLE delivers mail within seconds; servers without IDLE are polled (every minute by default).
- **Rich emails** — emails are sent as [rich messages](https://core.telegram.org/bots/api#rich-messages) (Bot API 10.1+) with headings, lists, links, collapsible quotes and "read more" for long emails; dates are shown in each reader's time zone. Falls back to HTML messages, then to plain text, automatically.
- **Verification codes** are detected and get a one-tap copy button.
- **Buttons** — "Read" sets `\Seen` in the mailbox, "Delete" (with confirmation) moves the email to Trash. Only group admins can press them.
- **No lost mail** — if Telegram is down the email is retried; IMAP reconnects automatically and the topic is notified only about lasting problems.
- **IMAP auto-detection** for popular providers and custom domains (via MX).
- **Mailcow** — create mailboxes from Telegram with `/create` (optional).
- **Security** — passwords are encrypted with AES-256-GCM, messages containing passwords are deleted immediately, forwarding does not mark mail as read.

### Quick start

```bash
git clone https://github.com/mixelka75/tgEmailResend.git
cd tgEmailResend
cp .env.example .env
make generate-key       # put the output into ENCRYPTION_KEY
make docker-run         # or: make build && ./emailbot
```

Set `TELEGRAM_BOT_TOKEN` and `ENCRYPTION_KEY` (exactly 32 characters) in `.env`. Do not change `ENCRYPTION_KEY` after connecting mailboxes.

Create a group, enable **Topics**, add the bot as an **admin with the "Delete messages" right**, open a topic and send `/connect email password`.

### Commands

| Command | Description |
|---------|-------------|
| `/connect email password [host:port]` | Connect a mailbox to the current topic (993 = TLS, 143 = STARTTLS) |
| `/create name [password] [Full Name]` | Create a Mailcow mailbox and connect it |
| `/disconnect` | Disconnect the mailbox from the topic |
| `/status` | Mailboxes of the group and their state |
| `/help` | Help |

### Providers

Gmail, Yandex, Mail.ru and iCloud need an **app password**. Proton Mail works through Proton Mail Bridge (`127.0.0.1:1143`). **Outlook/Microsoft 365 is not supported**: Microsoft disabled password authentication for IMAP (OAuth2 only).

### Mailcow

Create a **read-write** API key in Mailcow (System → Configuration → Access → Edit administrator → API) and allow the bot's IP. Then set `MAILCOW_URL`, `MAILCOW_API_KEY`, `MAILCOW_DOMAIN`. Optional: `MAILCOW_IMAP_SERVER`, `MAILCOW_MAILBOX_QUOTA_MB`. Mailcow reports failed API logins to fail2ban, so do not retry `/create` repeatedly with a wrong key.

### Configuration

See the table in the Russian section and comments in [.env.example](.env.example).

## License

MIT
