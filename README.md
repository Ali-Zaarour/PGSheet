# PGSheet

[![Latest release](https://img.shields.io/github/v/release/Ali-Zaarour/PGSheet?include_prereleases&label=download&color=2563eb)](https://github.com/Ali-Zaarour/PGSheet/releases)

Turn an Excel sheet into a PostgreSQL insert script you can read before you run it.

## Download

**[Download the latest version for Windows](https://github.com/Ali-Zaarour/PGSheet/releases)**

One file, about 250MB. It carries everything it needs, including the Microsoft
WebView2 runtime, so it installs on a machine with no internet. Windows asks
for administrator rights once, to install that runtime.

The smaller `.zip` on the same page is the bare application, for a machine that
already has WebView2.

## The problem

Loading a client's spreadsheet into a table is routine and repeatedly painful.
Writing the SQL by hand works for twenty rows, not two thousand. Import tools
stop at the first bad row with a message like
`invalid input syntax for type integer: ""` and no clue which of the 3,000 rows
it means.

The traps are never visible in the spreadsheet: "N/A" in a phone column, a date
typed as "March 2024", a status of "Actif" where the database only takes
`active`, two rows sharing an email that has to be unique. Each one stops the
import, and each is found one at a time, with a full retry in between.

## How it works

1. **Connect** to the database.
2. **Pick the table.** Its real structure is read live: columns, types, limits,
   defaults, constraints.
3. **Open the spreadsheet** and confirm the sheet and header row.
4. **Map the columns.** Matching names are paired for you. Per column you can
   trim spaces, set a date format, or say which words mean true and false.
5. **Choose the key.** Let the database assign it, or take it from the sheet
   and have the sequence resynchronised so the live application keeps working.
6. **Validate.** Every cell, against the table's real rules.
7. **Generate** the `.sql` file.

Save the setup to a small configuration file, and next month's import takes
minutes instead of an afternoon.

## What it promises

- **Nothing is written by accident.** The output is a file. The database is
  touched only if you ask for it, and only after typing the table name.
- **All or nothing.** Generated files run inside a transaction, so a failure
  part way through leaves the table exactly as it was.
- **Every error points at something.** A row, a column, the value and the
  reason. A column mapped wrong says so once, not nine hundred times.
- **Nothing is stored.** No saved credentials, no history, no embedded
  database. Only the files you choose to save.

## Limits

One table per run. `.xlsx` only, one sheet at a time. Inserts only, no updates
or upserts. PostgreSQL only.

## What you need

Windows, and a PostgreSQL database you can reach with an account that can read
the target table. Nothing else: the installer carries everything it needs, so
it works on a machine with no internet.

---

Version 1.0.0-beta, a first test build.

Ali Zaarour · zaarour.a@outlook.com · +96103979874
