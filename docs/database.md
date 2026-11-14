# Moving the database

Everything Box Office keeps (your movies, shows, music, books and games, downloads and their history, indexers, news servers, settings, members and requests) lives in its database: a SQLite file by default, or PostgreSQL. Box Office can move all of it to another database for you, for example when you'd rather keep it in the PostgreSQL server your other apps use, or when PostgreSQL moves to a NAS or another server.

It works in any direction:

- **SQLite to PostgreSQL**
- **PostgreSQL to another PostgreSQL**, such as from the Helm chart's bundled database to one on your NAS
- **PostgreSQL back to a SQLite file**

## How a move works

1. Box Office makes its own tables in the new database, with the same schema it uses itself, so the new database is exactly what this version of Box Office expects.
2. It copies every table as it was at one moment, including sign-ins, so nobody is signed out.
3. It counts the rows of every table in both databases. If any table differs, nothing is switched over.
4. It switches over and opens on the new database.

The tables and the copy go in as one transaction, so a move that fails leaves nothing half done in the new database, and Box Office carries on with the database it had.

**The old database keeps all its data.** Box Office only adds a note to it saying where the database went, so a Box Office whose settings still name the old database finds the new one. Keep the old database until you've [changed the settings](#after-the-move) and you're happy with the new one.

## Before you start

The new database must be **empty**:

- **PostgreSQL**: make an empty database owned by the user Box Office signs in as. Box Office makes the tables itself. With `psql`:

  ```sql
  CREATE USER boxoffice WITH PASSWORD 'a long password';
  CREATE DATABASE boxoffice OWNER boxoffice;
  ```

  Its address looks like `postgres://boxoffice:a%20long%20password@tower.local:5432/boxoffice?sslmode=disable`. Characters such as spaces and `@` in the password are written as `%20` and `%40`. Any recent PostgreSQL works; the Helm chart bundles PostgreSQL 17.
- **SQLite**: the full path of a file that doesn't exist yet, on a disk that lasts, such as `/data/boxoffice.db` in the data folder.

## In Settings

Move the database in **Settings → General → Database** (`/settings#database`), which says which database Box Office uses now.

1. Pick **PostgreSQL** or **SQLite file** and type the new database's address or file.
2. Press **Check**. Box Office connects to it and makes sure it isn't the database in use and has none of Box Office's data yet. Nothing is copied or changed. If something's wrong, such as a wrong password or a database that doesn't exist yet, it says so.
3. Press **Move database** and confirm. Box Office restarts the same way the **Restart** button does (downloads pause where they are and carry on after), moves the database before anything else uses it, so nothing written meanwhile is left behind, and opens on the new one. The page waits and loads again by itself; meanwhile Box Office's address shows how many rows are copied. Most libraries take seconds; a big one a minute or two.

If the server can't restart itself, the move waits for the next time you start Box Office. **Cancel move** keeps everything as it is until then.

If the move doesn't work, Box Office keeps using the database it had, unchanged, and Settings says why.

## After the move

Settings then shows where the database came from and how many rows were copied. Until you change Box Office's settings to the new database, it also says that they still name the old one: Box Office finds the new database through the note in the old one, so **the old one has to stay where it is until you change them**.

- **Docker or a binary**: set `BOXOFFICE_DATABASE_URL` to the new PostgreSQL address and restart Box Office. For a move back to SQLite, take `BOXOFFICE_DATABASE_URL` out; Box Office uses `boxoffice.db` in its data folder.
- **Kubernetes with the Helm chart**: see [below](#on-kubernetes).

Once Box Office runs with its settings naming the new database, you can remove the old one. Backups in **Backups** keep working the same way with either database.

## On Kubernetes

Box Office's data folder is on its own volume, so the SQLite file and the note in it survive the pod being replaced. To move to a PostgreSQL server, such as one on your NAS:

1. Make an empty database there, as [above](#before-you-start), that the cluster can reach.
2. Move to it in **Settings → General → Database**. Box Office's pod restarts in place and opens on the new database.
3. Put the new database in the chart's values, and turn the bundled one off if you used it:

   ```yaml
   database:
     # a Secret with the address under "database-url", or url: postgres://…
     existingSecret: boxoffice-database
   postgresql:
     enabled: false
   ```

   With Argo CD, commit that and sync. Box Office restarts on the new database directly.
4. The old `boxoffice.db` stays in the data volume (and a bundled PostgreSQL's volume is kept when the chart turns it off). Remove it once you're happy.

## From the command line

`boxoffice database` does the same with the server stopped, which suits a server you can't reach in a browser, or a script:

```sh
boxoffice database where
boxoffice database move --to 'postgres://boxoffice:password@tower.local:5432/boxoffice?sslmode=disable'
```

Stop the Box Office server first: anything it writes during the move isn't copied. Give the same settings the server uses, such as `--data /data` or `BOXOFFICE_DATABASE_URL`, so the command finds the database it uses now. It prints each table's rows once they're checked. `--copy-only` copies and checks without switching over, to try a move first. In Docker, run it in a stopped container's place with the same volumes and environment, such as `docker compose run --rm boxoffice database move --to …`.

### Going back

`boxoffice database undo-move` takes the note out of the old database, so Box Office uses it again. Anything that changed in the new database since the move isn't in the old one; the new database is left as it is.
