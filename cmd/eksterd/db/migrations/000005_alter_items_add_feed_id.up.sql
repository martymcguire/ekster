ALTER TABLE "items" ADD COLUMN "feed_id" INT REFERENCES "feeds" ON DELETE CASCADE;