# rapira_rates

gRPC-сервис получает стакан USDT/RUB с Rapira через resty, рассчитывает ask и bid и сохраняет результат с временем получения в PostgreSQL. Каждый вызов `GetRates` запрашивает свежие данные. Вместо Grinex используется Rapira, поэтому торговая пара изменена с USDT/A7A5 на USDT/RUB.

## Запуск

Нужны Docker с Compose и `make`. Выполнять из корня проекта; локальный Go для запуска не нужен.

```sh
make up                       # БД → миграция → сборка и запуск сервиса с Jaeger
docker compose logs -f app
```

gRPC: `localhost:50052`, PostgreSQL: `localhost:15433`, Jaeger: http://localhost:16686.

```sh
docker compose stop           # Остановка с сохранением данных
```

Миграцию запускает `make up`; обычный `docker compose up` её не применяет. Для повторного запуска используйте `make up`.

## Настройки

Параметры можно передать флагами или переменными окружения. Флаги имеют приоритет.

| Переменная | Флаг | По умолчанию |
| --- | --- | --- |
| `DATABASE_URL` | `--database-url` | `postgres://rates:rates_local@localhost:15433/rapira_rates?sslmode=disable` |
| `GRPC_ADDRESS` | `--grpc-address` | `:50052` |
| `STARTUP_TIMEOUT` | `--startup-timeout` | `5s` |
| `SHUTDOWN_TIMEOUT` | `--shutdown-timeout` | `5s` |

`DATABASE_URL` задаёт пользователя, пароль, адрес и имя БД. В Docker настройки находятся в `services.app.environment` файла `docker-compose.yml`; адрес БД там — `postgres:5432`.

Для локального запуска нужен Go 1.26.2 или новее. Сначала запустите БД и Jaeger:

```sh
make db-up
make migrate-up
docker compose up -d jaeger
```

Затем запустите приложение. Если контейнер `app` уже работает, остановите его командой `docker compose stop app`, чтобы освободить порт.

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make run
```

Примеры настройки подключения к БД через переменную и через флаг:

```sh
DATABASE_URL='postgres://rates:rates_local@localhost:15433/rapira_rates?sslmode=disable' \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make run

OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
  make run ARGS='--database-url=postgres://rates:rates_local@localhost:15433/rapira_rates?sslmode=disable'
```

## Запросы

Для вызовов нужен `grpcurl`. Позиции в стакане начинаются с единицы. Ask и bid рассчитываются отдельно.

`topN` — цена на позиции N. Например, получить первый уровень:

```sh
grpcurl -plaintext -d '{"method":"CALCULATION_METHOD_TOP_N","n":1}' \
  localhost:50052 rate.v1.RateService/GetRates
```

`avgNM` — средняя цена от N до M включительно, без учёта объёмов заявок. Например, среднее первых трёх уровней:

```sh
grpcurl -plaintext -d '{"method":"CALCULATION_METHOD_AVG_NM","n":1,"m":3}' \
  localhost:50052 rate.v1.RateService/GetRates
```

В ответе приходят торговая пара, ask, bid, время получения в UTC и параметры расчёта. Цены передаются строками. При успешном запросе результат уже сохранён в БД.

Для `topN` параметр M не задаётся или равен нулю. Для `avgNM` должно выполняться `1 <= N <= M`. Неверные параметры возвращают `InvalidArgument`, нехватка уровней — `FailedPrecondition`.

Проверка работоспособности:

```sh
grpcurl -plaintext -d '{"service":""}' localhost:50052 grpc.health.v1.Health/Check
```

Healthcheck показывает состояние gRPC-сервера; доступность БД и биржи при каждом вызове он не проверяет.

## Тесты и сборка

Нужен Go 1.26.2 или новее, для линтера — совместимый с ним `golangci-lint` v2.

```sh
make test                     # Тесты
make test-race                # Тесты с проверкой гонок
make lint                     # Линтер
make build                    # Сборка в bin/app
make docker-build             # Сборка Docker-образа
```

## Трассировка и остановка

После запроса откройте [Jaeger](http://localhost:16686), выберите сервис `rapira_rates` и нажмите **Find Traces**. OpenTelemetry показывает обработку gRPC-запроса, обращение к Rapira и сохранение в БД. Трассы могут появиться через несколько секунд.

При SIGINT/SIGTERM сервис прекращает принимать запросы и ждёт завершения текущих до `SHUTDOWN_TIMEOUT`. Затем прерывает оставшиеся, отправляет накопленные трассы и закрывает подключение к БД.
