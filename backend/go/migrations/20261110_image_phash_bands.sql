-- Go-only helper for indexed perceptual-hash near-duplicate lookup.
-- Fixes upload timeouts caused by full-table Hamming-distance scans on Images.
--
-- Safe to run multiple times (IF NOT EXISTS / ON CONFLICT).
-- Apply to production/Supabase, then backfill. The backend will use the
-- band index when this table is present and populated (and falls back to
-- the legacy scan for any rows not yet banded).

CREATE TABLE IF NOT EXISTS "ImagePhashBands" (
    "ImageId" bigint NOT NULL PRIMARY KEY REFERENCES "Images"("Id") ON DELETE CASCADE,
    "Band1"   integer NOT NULL,
    "Band2"   integer NOT NULL,
    "Band3"   integer NOT NULL,
    "Band4"   integer NOT NULL,
    "Band5"   integer NOT NULL
);

CREATE INDEX IF NOT EXISTS "IX_ImagePhashBands_Band1" ON "ImagePhashBands" ("Band1");
CREATE INDEX IF NOT EXISTS "IX_ImagePhashBands_Band2" ON "ImagePhashBands" ("Band2");
CREATE INDEX IF NOT EXISTS "IX_ImagePhashBands_Band3" ON "ImagePhashBands" ("Band3");
CREATE INDEX IF NOT EXISTS "IX_ImagePhashBands_Band4" ON "ImagePhashBands" ("Band4");
CREATE INDEX IF NOT EXISTS "IX_ImagePhashBands_Band5" ON "ImagePhashBands" ("Band5");

-- Backfill bands from Images.PerceptualHash.
-- Band split must match models.PhashBands (LSB-first): 13,13,13,13,12 bits.
INSERT INTO "ImagePhashBands" ("ImageId", "Band1", "Band2", "Band3", "Band4", "Band5")
SELECT
    i."Id",
    (
        get_bit(i."PerceptualHash", 63 - 0)  * 1 +
        get_bit(i."PerceptualHash", 63 - 1)  * 2 +
        get_bit(i."PerceptualHash", 63 - 2)  * 4 +
        get_bit(i."PerceptualHash", 63 - 3)  * 8 +
        get_bit(i."PerceptualHash", 63 - 4)  * 16 +
        get_bit(i."PerceptualHash", 63 - 5)  * 32 +
        get_bit(i."PerceptualHash", 63 - 6)  * 64 +
        get_bit(i."PerceptualHash", 63 - 7)  * 128 +
        get_bit(i."PerceptualHash", 63 - 8)  * 256 +
        get_bit(i."PerceptualHash", 63 - 9)  * 512 +
        get_bit(i."PerceptualHash", 63 - 10) * 1024 +
        get_bit(i."PerceptualHash", 63 - 11) * 2048 +
        get_bit(i."PerceptualHash", 63 - 12) * 4096
    )::int AS "Band1",
    (
        get_bit(i."PerceptualHash", 63 - 13) * 1 +
        get_bit(i."PerceptualHash", 63 - 14) * 2 +
        get_bit(i."PerceptualHash", 63 - 15) * 4 +
        get_bit(i."PerceptualHash", 63 - 16) * 8 +
        get_bit(i."PerceptualHash", 63 - 17) * 16 +
        get_bit(i."PerceptualHash", 63 - 18) * 32 +
        get_bit(i."PerceptualHash", 63 - 19) * 64 +
        get_bit(i."PerceptualHash", 63 - 20) * 128 +
        get_bit(i."PerceptualHash", 63 - 21) * 256 +
        get_bit(i."PerceptualHash", 63 - 22) * 512 +
        get_bit(i."PerceptualHash", 63 - 23) * 1024 +
        get_bit(i."PerceptualHash", 63 - 24) * 2048 +
        get_bit(i."PerceptualHash", 63 - 25) * 4096
    )::int AS "Band2",
    (
        get_bit(i."PerceptualHash", 63 - 26) * 1 +
        get_bit(i."PerceptualHash", 63 - 27) * 2 +
        get_bit(i."PerceptualHash", 63 - 28) * 4 +
        get_bit(i."PerceptualHash", 63 - 29) * 8 +
        get_bit(i."PerceptualHash", 63 - 30) * 16 +
        get_bit(i."PerceptualHash", 63 - 31) * 32 +
        get_bit(i."PerceptualHash", 63 - 32) * 64 +
        get_bit(i."PerceptualHash", 63 - 33) * 128 +
        get_bit(i."PerceptualHash", 63 - 34) * 256 +
        get_bit(i."PerceptualHash", 63 - 35) * 512 +
        get_bit(i."PerceptualHash", 63 - 36) * 1024 +
        get_bit(i."PerceptualHash", 63 - 37) * 2048 +
        get_bit(i."PerceptualHash", 63 - 38) * 4096
    )::int AS "Band3",
    (
        get_bit(i."PerceptualHash", 63 - 39) * 1 +
        get_bit(i."PerceptualHash", 63 - 40) * 2 +
        get_bit(i."PerceptualHash", 63 - 41) * 4 +
        get_bit(i."PerceptualHash", 63 - 42) * 8 +
        get_bit(i."PerceptualHash", 63 - 43) * 16 +
        get_bit(i."PerceptualHash", 63 - 44) * 32 +
        get_bit(i."PerceptualHash", 63 - 45) * 64 +
        get_bit(i."PerceptualHash", 63 - 46) * 128 +
        get_bit(i."PerceptualHash", 63 - 47) * 256 +
        get_bit(i."PerceptualHash", 63 - 48) * 512 +
        get_bit(i."PerceptualHash", 63 - 49) * 1024 +
        get_bit(i."PerceptualHash", 63 - 50) * 2048 +
        get_bit(i."PerceptualHash", 63 - 51) * 4096
    )::int AS "Band4",
    (
        get_bit(i."PerceptualHash", 63 - 52) * 1 +
        get_bit(i."PerceptualHash", 63 - 53) * 2 +
        get_bit(i."PerceptualHash", 63 - 54) * 4 +
        get_bit(i."PerceptualHash", 63 - 55) * 8 +
        get_bit(i."PerceptualHash", 63 - 56) * 16 +
        get_bit(i."PerceptualHash", 63 - 57) * 32 +
        get_bit(i."PerceptualHash", 63 - 58) * 64 +
        get_bit(i."PerceptualHash", 63 - 59) * 128 +
        get_bit(i."PerceptualHash", 63 - 60) * 256 +
        get_bit(i."PerceptualHash", 63 - 61) * 512 +
        get_bit(i."PerceptualHash", 63 - 62) * 1024 +
        get_bit(i."PerceptualHash", 63 - 63) * 2048
    )::int AS "Band5"
FROM "Images" i
ON CONFLICT ("ImageId") DO UPDATE
SET "Band1" = EXCLUDED."Band1",
    "Band2" = EXCLUDED."Band2",
    "Band3" = EXCLUDED."Band3",
    "Band4" = EXCLUDED."Band4",
    "Band5" = EXCLUDED."Band5";
