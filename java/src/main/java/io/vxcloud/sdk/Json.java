package io.vxcloud.sdk;

/**
 * Json — a deliberately tiny scalar-field extractor used internally by
 * {@link VxClient} for the only two things it must parse itself: tokens on
 * login and the node address during discovery.
 *
 * <p>This is NOT a general JSON parser. Data methods on {@link VxClient}
 * return the raw response body so callers can use Jackson/Gson/etc.
 */
final class Json {
    private Json() {}

    /**
     * Return the string (or scalar) value of the first-seen {@code "name"} key.
     * Handles backslash escapes inside string values. Returns "" if absent.
     */
    static String field(String doc, String name) {
        if (doc == null) return "";
        String needle = "\"" + name + "\"";
        int p = 0;
        while ((p = doc.indexOf(needle, p)) >= 0) {
            int i = p + needle.length();
            i = skipWs(doc, i);
            if (i >= doc.length() || doc.charAt(i) != ':') { p += needle.length(); continue; }
            i = skipWs(doc, i + 1);
            if (i >= doc.length()) return "";
            if (doc.charAt(i) == '"') {
                i++;
                StringBuilder out = new StringBuilder();
                while (i < doc.length() && doc.charAt(i) != '"') {
                    char c = doc.charAt(i);
                    if (c == '\\' && i + 1 < doc.length()) {
                        char n = doc.charAt(i + 1);
                        switch (n) {
                            case 'n': out.append('\n'); break;
                            case 't': out.append('\t'); break;
                            case 'r': out.append('\r'); break;
                            default:  out.append(n);
                        }
                        i += 2;
                    } else {
                        out.append(c);
                        i++;
                    }
                }
                return out.toString();
            }
            // scalar value
            StringBuilder out = new StringBuilder();
            while (i < doc.length()) {
                char c = doc.charAt(i);
                if (c == ',' || c == '}' || c == ']' || c == ' ' || c == '\n'
                        || c == '\r' || c == '\t') break;
                out.append(c);
                i++;
            }
            return out.toString();
        }
        return "";
    }

    private static int skipWs(String s, int i) {
        while (i < s.length()) {
            char c = s.charAt(i);
            if (c != ' ' && c != '\t' && c != '\n' && c != '\r') break;
            i++;
        }
        return i;
    }
}
