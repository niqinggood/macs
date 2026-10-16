import threading
import unittest

from inventory import Inventory


class InventoryTests(unittest.TestCase):
    def test_rejects_non_positive_quantity(self):
        inv = Inventory({"A": 3})
        with self.assertRaises(ValueError):
            inv.reserve("A", 0)

    def test_concurrent_reservations_do_not_oversell(self):
        inv = Inventory({"A": 5})
        barrier = threading.Barrier(10)
        outcomes = []
        result_lock = threading.Lock()

        def worker():
            barrier.wait()
            result = inv.reserve("A", 1)
            with result_lock:
                outcomes.append(result)

        threads = [threading.Thread(target=worker) for _ in range(10)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()

        self.assertEqual(5, outcomes.count(True))
        self.assertEqual(5, outcomes.count(False))
        self.assertEqual(0, inv.snapshot()["A"])

    def test_different_skus_can_progress_independently(self):
        inv = Inventory({"A": 1, "B": 1})
        barrier = threading.Barrier(2)
        outcomes = []

        def worker(sku):
            barrier.wait()
            outcomes.append(inv.reserve(sku, 1))

        first = threading.Thread(target=worker, args=("A",))
        second = threading.Thread(target=worker, args=("B",))
        first.start()
        second.start()
        first.join()
        second.join()
        self.assertEqual([True, True], sorted(outcomes))


if __name__ == "__main__":
    unittest.main()
