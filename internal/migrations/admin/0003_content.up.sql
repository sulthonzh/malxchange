-- 0003_content: the CMS (ticket #63) — announcements and the help
-- center (categories + articles), admin-api's own tables. Members read
-- them only through admin-api's internal content RPC, whose wire shape
-- is pinned by internal/api/content_CONTRACT.md; api proxies it with
-- zero api-side changes. English-only content (ADR-0013), stored as
-- the literal HTML document the editor saved.

CREATE TABLE announcements (
    id           TEXT PRIMARY KEY,        -- uuid (the legacy numeric ids do not survive)
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,           -- the stored HTML document
    published_at TIMESTAMPTZ,             -- NULL = draft; members see published rows only
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- The member listing is newest-first among published rows (the
-- contract's news order); id breaks ties deterministically.
CREATE INDEX announcements_published_idx
    ON announcements (published_at DESC, id DESC);

CREATE TABLE help_categories (
    id         TEXT PRIMARY KEY,          -- uuid; the member surface's opaque cate
    title      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE help_articles (
    id           TEXT PRIMARY KEY,        -- uuid
    category_id  TEXT NOT NULL REFERENCES help_categories(id),
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,           -- the stored HTML document
    published_at TIMESTAMPTZ,             -- NULL = draft; members see published rows only
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- A category's articles serve oldest-first (reading order, the help
-- center's order — distinct from the announcements' news order).
CREATE INDEX help_articles_category_idx
    ON help_articles (category_id, created_at, id);
