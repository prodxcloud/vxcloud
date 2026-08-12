package io.vxcloud.sdk;

/**
 * VxException — the single error type raised by {@link VxClient}.
 *
 * <p>Carries the failing operation, an HTTP status (0 for transport failures),
 * and up to ~800 bytes of server-supplied detail. Branch with {@link #isAuth()}
 * (401/403), {@link #isRetryable()} (0/429/5xx), or {@link #httpStatus()}.
 */
public class VxException extends RuntimeException {
    private final int httpStatus;
    private final String op;
    private final String detail;

    public VxException(String op, String message, int httpStatus, String detail) {
        super(compose(op, message, httpStatus, detail));
        this.op = op;
        this.httpStatus = httpStatus;
        this.detail = detail == null ? "" : detail;
    }

    public int httpStatus()   { return httpStatus; }
    public String op()        { return op; }
    public String detail()    { return detail; }
    public boolean isAuth()   { return httpStatus == 401 || httpStatus == 403; }
    public boolean isRetryable() { return httpStatus == 0 || httpStatus == 429 || httpStatus >= 500; }

    private static String compose(String op, String message, int status, String detail) {
        StringBuilder b = new StringBuilder(op).append(": ");
        if (status != 0) b.append(status).append(' ');
        b.append(message);
        if (detail != null && !detail.isEmpty()) b.append(" — ").append(detail);
        return b.toString();
    }
}
