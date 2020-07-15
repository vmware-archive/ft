FROM ubuntu:bionic
COPY postgresql-11-bionic /tmp/postgresql-11-bionic
RUN apt update && apt install /tmp/postgresql-11-bionic/postgresql-*.deb
