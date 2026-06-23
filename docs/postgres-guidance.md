# Postgres Provisioning for Beelink Homelab

This document describes how to provision a Postgres database to run Polar on a Beelink homelab stack.

## Recommended Version

Use **PostgreSQL 14 or later**. On Debian-based systems you can install via apt:

```
sudo apt update
sudo apt install postgresql
```

## Database Setup

1. Create a role and database:

```
sudo -u postgres createuser --interactive
sudo -u postgres createdb polar
```

2. Set a password for the `polar` user:

```
sudo -u postgres psql -c "ALTER USER polar WITH PASSWORD '<strongpassword>';"
```

3. Update `configs/polar.json` or set the `POLAR_DATABASE_URL` environment variable to point at your database, e.g.:

```
postgresql://polar:<password>@localhost:5432/polar?sslmode=disable
```

4. Ensure that `storage.driver` is set to `postgres` in your configuration.

## Resource Tuning

Beelink devices are low‑power; adjust Postgres configuration accordingly (e.g., in `postgresql.conf`):

```
max_connections = 20
shared_buffers = 128MB
work_mem = 4MB
maintenance_work_mem = 32MB
```

## Backups

Set up regular backups using `pg_dump` or `pg_basebackup`. On a small homelab, a simple cron job like:

```
0 3 * * * pg_dump -U polar -F c -f /var/backups/polar_$(date +\%Y\%m\%d).dump polar
```

will keep daily backups.

## Running Postgres with Docker

Alternatively, you can run Postgres in a container using docker or docker-compose:

```
version: "3.9"
services:
  db:
    image: postgres:15
    environment:
      POSTGRES_USER: polar
      POSTGRES_PASSWORD: <password>
      POSTGRES_DB: polar
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    ports:
      - "5432:5432"
```

This container exposes Postgres on port 5432 and persists data to `./data/postgres`.

---

This document aims to guide homelab users running Polar on a Beelink device, but the general approach applies to any self‑hosted deployment.
