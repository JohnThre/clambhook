namespace Clambhook {
    public class Formatters {
        public static string format_rate(double bytes_per_second) {
            string[] units = { "B/s", "KB/s", "MB/s", "GB/s" };
            var value = bytes_per_second;
            var unit = 0;
            while (value >= 1024 && unit < units.length - 1) {
                value /= 1024;
                unit++;
            }
            return unit == 0 ? "%d %s".printf((int) value, units[unit]) : "%.1f %s".printf(value, units[unit]);
        }

        public static string format_bytes(uint64 bytes) {
            string[] units = { "B", "KB", "MB", "GB" };
            var value = (double) bytes;
            var unit = 0;
            while (value >= 1024 && unit < units.length - 1) {
                value /= 1024;
                unit++;
            }
            return unit == 0 ? "%s %s".printf(bytes.to_string(), units[unit]) : "%.1f %s".printf(value, units[unit]);
        }

        public static string format_duration_ns(int64 ns) {
            if (ns <= 0) {
                return "--";
            }
            var seconds = ns / 1000000000;
            if (seconds < 1) {
                return "%s ms".printf((ns / 1000000).to_string());
            }
            if (seconds < 60) {
                return "%s s".printf(seconds.to_string());
            }
            return "%s min".printf((seconds / 60).to_string());
        }

        public static string server_location(ServerPayload server) {
            if (server.geo.city != "" && server.geo.country != "") {
                return "%s, %s".printf(server.geo.city, server.geo.country);
            }
            if (server.geo.country != "") {
                return server.geo.country;
            }
            return server.address;
        }
    }
}
