"""Start the worker with structured operational logs for Loki collection."""
import datetime
import json
import logging
import sys
import uvicorn

class JSONFormatter(logging.Formatter):
    def format(self, record):
        value = {"time": datetime.datetime.fromtimestamp(record.created, datetime.timezone.utc).isoformat(), "level": record.levelname, "msg": record.getMessage(), "logger": record.name}
        if record.exc_info:
            value["exception"] = self.formatException(record.exc_info)
        return json.dumps(value, ensure_ascii=False)

def main():
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JSONFormatter())
    logging.basicConfig(level=logging.INFO, handlers=[handler], force=True)
    for name in ("uvicorn", "uvicorn.error", "uvicorn.access"):
        logger = logging.getLogger(name)
        logger.handlers.clear()
        logger.propagate = True
    uvicorn.run("gofi.server:create_app", factory=True, host="0.0.0.0", port=8080, log_config=None, access_log=False)

if __name__ == "__main__":
    main()
