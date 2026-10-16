import sqlite3


def ensure_schema(db):
    db.execute("CREATE TABLE IF NOT EXISTS customers (id INTEGER PRIMARY KEY, full_name TEXT NOT NULL)")


def create_customer(db, payload):
    full_name = payload["full_name"]
    cursor = db.execute("INSERT INTO customers(full_name) VALUES (?)", (full_name,))
    db.commit()
    return {"id": cursor.lastrowid, "full_name": full_name}


def open_memory_db():
    db = sqlite3.connect(":memory:")
    ensure_schema(db)
    return db
