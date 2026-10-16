import time


class Inventory:
    def __init__(self, initial=None):
        self._stock = dict(initial or {})

    def reserve(self, sku, quantity):
        if quantity <= 0:
            raise ValueError("quantity must be positive")
        current = self._stock.get(sku, 0)
        if current < quantity:
            return False
        # 模拟数据库往返，放大“检查后再扣减”的竞争窗口。
        time.sleep(0.01)
        self._stock[sku] = current - quantity
        return True

    def restock(self, sku, quantity):
        if quantity <= 0:
            raise ValueError("quantity must be positive")
        current = self._stock.get(sku, 0)
        time.sleep(0.002)
        self._stock[sku] = current + quantity

    def snapshot(self):
        return dict(self._stock)
