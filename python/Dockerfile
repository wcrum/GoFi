FROM python:3.14.7-slim-bookworm@sha256:82bc3c539b8813ada9d68c63b40158fa002f7f33de9bf3312a3dfdc0620dff56
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /opt/gofi
COPY python/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt
COPY python/gofi ./gofi
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["python", "-m", "gofi.serve"]
