import concurrent.futures


class Network:
    def __init__(self, name, sequencers):
        self.name = name
        self.sequencers = sequencers

    def update(self):
        def _update(sequencer):
            sequencer.update()
        with concurrent.futures.ThreadPoolExecutor() as executor:
            list(executor.map(_update, self.sequencers))

    def get_sequencer_by_id(self, sequencer_id: str):
        return next(
            (
                sequencer
                for sequencer in self.sequencers
                if sequencer.sequencer_id == sequencer_id
            ),
            None,
        )

    def find_conductor_leader(self):
        return next(
            (sequencer for sequencer in self.sequencers if sequencer.conductor_leader),
            None,
        )

    def find_active_sequencer(self):
        return next(
            (sequencer for sequencer in self.sequencers if sequencer.sequencer_active),
            None,
        )
