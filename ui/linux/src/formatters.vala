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
