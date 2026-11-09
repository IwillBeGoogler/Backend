# Postgres backup sidecar: pg_dump on a schedule into /backups, optional rclone upload (S3, MinIO, R2, B2 ...).
FROM postgres:18-alpine
RUN apk add --no-cache rclone tzdata \
 && mkdir /backups && chown postgres:postgres /backups
COPY backup.sh /usr/local/bin/backup.sh
USER postgres
VOLUME /backups
ENTRYPOINT ["/bin/sh", "/usr/local/bin/backup.sh"]
