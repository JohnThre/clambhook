namespace Clambhook {
    public class EventStreamClient : Object {
        private Soup.Session session;
        private Soup.WebsocketConnection? connection;
        private Cancellable? cancellable;
        private uint generation = 0;

        public signal void event_received(DaemonEvent event);
        public signal void stream_failed(string message);
        public signal void closed();

        public EventStreamClient() {
            session = new Soup.Session();
        }

        public void start(string uri, string authorization) {
            stop();
            generation++;
            var current_generation = generation;
            cancellable = new Cancellable();

            var message = new Soup.Message("GET", uri);
            if (message == null) {
                stream_failed("invalid event stream URL");
                return;
            }
            if (authorization != "") {
                message.request_headers.append("Authorization", authorization);
            }

            session.websocket_connect_async(message, null, null, Priority.DEFAULT, cancellable, (obj, res) => {
                if (current_generation != generation) {
                    return;
                }
                try {
                    connection = session.websocket_connect_finish(res);
                    connection.message.connect((type, bytes) => {
                        if (current_generation == generation) {
                            on_message(type, bytes);
                        }
                    });
                    connection.closed.connect(() => {
                        if (current_generation != generation) {
                            return;
                        }
                        connection = null;
                        closed();
                    });
                } catch (Error err) {
                    if (current_generation != generation) {
                        return;
                    }
                    connection = null;
                    stream_failed(err.message);
                }
            });
        }

        public void stop() {
            generation++;
            if (cancellable != null) {
                cancellable.cancel();
                cancellable = null;
            }
            if (connection != null) {
                connection.close(Soup.WebsocketCloseCode.NORMAL, null);
                connection = null;
            }
        }

        private void on_message(Soup.WebsocketDataType type, Bytes bytes) {
            if (type != Soup.WebsocketDataType.TEXT) {
                return;
            }
            size_t size = 0;
            unowned uint8[] data = bytes.get_data(out size);
            var event = DaemonEvent.from_json((string) data);
            if (event.type != "") {
                event_received(event);
            }
        }
    }
}
