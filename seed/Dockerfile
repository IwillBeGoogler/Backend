# Dev-only demo data, see seed.py.
FROM python:3.13-alpine
RUN apk add --no-cache postgresql-client
COPY . /seed
WORKDIR /seed
ENTRYPOINT ["python3", "seed.py"]
