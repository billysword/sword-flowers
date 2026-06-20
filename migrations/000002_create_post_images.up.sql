CREATE TABLE post_images (
    id         BIGSERIAL    PRIMARY KEY,
    post_id    UUID         NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    image_ref  TEXT         NOT NULL,
    caption    TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX post_images_post_id_idx ON post_images (post_id);
