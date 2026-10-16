"""
AI请求支付服务 - FastAPI后台程序
提供用户充值、扣费、鉴权接口
按次收费，费用不足时提示用户充值
"""

import json
import time
import uuid
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Union
from pathlib import Path

from fastapi import FastAPI, HTTPException, Depends, Header, status
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from pydantic import BaseModel, Field, validator
import uvicorn# ==================== 数据模型 ====================

class User(BaseModel):
    """用户模型"""
    user_id: str
    username: str
    email: Optional[str] = None
    phone: Optional[str] = None
    balance: float = 0.0  # 账户余额（元）
    created_at: str
    updated_at: str
    is_active: bool = True

class Transaction(BaseModel):
    """交易记录模型"""
    transaction_id: str
    user_id: str
    type: str  # 'recharge' 充值, 'deduction' 扣费, 'refund' 退款
    amount: float
    description: str
    status: str  # 'pending', 'completed', 'failed'
    created_at: str
    reference_id: Optional[str] = None  # 外部参考ID

class AIRequest(BaseModel):
    """AI请求记录"""
    request_id: str
    user_id: str
    service_type: str  # 服务类型：chat, image, audio, etc.
    cost: float  # 本次请求费用
    status: str  # 'pending', 'processing', 'completed', 'failed'
    created_at: str
    completed_at: Optional[str] = None
    metadata: Optional[Dict] = None

class RechargeRequest(BaseModel):
    """充值请求"""
    amount: float = Field(..., gt=0, description="充值金额，必须大于0")
    payment_method: str = Field("alipay", description="支付方式：alipay, wechat, bank")
    description: Optional[str] = "用户充值"

class DeductionRequest(BaseModel):
    """扣费请求"""
    service_type: str = Field(..., description="服务类型：chat, image, audio, etc.")
    cost: float = Field(..., gt=0, description="扣费金额，必须大于0")
    metadata: Optional[Dict] = None

class AuthResponse(BaseModel):
    """鉴权响应"""
    authenticated: bool
    user_id: Optional[str] = None
    balance: Optional[float] = None
    message: str

class RechargeResponse(BaseModel):
    """充值响应"""
    success: bool
    transaction_id: str
    new_balance: float
    message: str

class DeductionResponse(BaseModel):
    """扣费响应"""
    success: bool
    request_id: str
    remaining_balance: float
    message: str# ==================== 数据库模拟 ====================

class Database:
    """模拟数据库"""
    
    def __init__(self):
        self.users: Dict[str, User] = {}
        self.transactions: Dict[str, Transaction] = {}
        self.ai_requests: Dict[str, AIRequest] = {}
        self.api_keys: Dict[str, str] = {}  # api_key -> user_id
        
        # 初始化测试用户
        self._init_test_data()
    
    def _init_test_data(self):
        """初始化测试数据"""
        now = datetime.now().isoformat()
        
        # 创建测试用户
        test_users = [
            User(
                user_id="user_001",
                username="test_user",
                email="test@example.com",
                phone="13800138000",
                balance=100.0,
                created_at=now,
                updated_at=now,
                is_active=True
            ),
            User(
                user_id="user_002",
                username="premium_user",
                email="premium@example.com",
                phone="13900139000",
                balance=500.0,
                created_at=now,
                updated_at=now,
                is_active=True
            ),
            User(
                user_id="user_003",
                username="low_balance_user",
                email="low@example.com",
                phone="13700137000",
                balance=5.0,
                created_at=now,
                updated_at=now,
                is_active=True
            )
        ]
        
        for user in test_users:
            self.users[user.user_id] = user
            # 为每个用户生成API密钥
            self.api_keys[f"api_key_{user.user_id}"] = user.user_id
    
    def get_user(self, user_id: str) -> Optional[User]:
        """获取用户"""
        return self.users.get(user_id)
    
    def update_user_balance(self, user_id: str, amount: float) -> bool:
        """更新用户余额"""
        if user_id not in self.users:
            return False
        
        user = self.users[user_id]
        user.balance += amount
        user.updated_at = datetime.now().isoformat()
        return True
    
    def add_transaction(self, transaction: Transaction) -> None:
        """添加交易记录"""
        self.transactions[transaction.transaction_id] = transaction
    
    def add_ai_request(self, request: AIRequest) -> None:
        """添加AI请求记录"""
        self.ai_requests[request.request_id] = request
    
    def get_user_transactions(self, user_id: str, limit: int = 10) -> List[Transaction]:
        """获取用户交易记录"""
        user_transactions = [
            t for t in self.transactions.values() 
            if t.user_id == user_id
        ]
        user_transactions.sort(key=lambda x: x.created_at, reverse=True)
        return user_transactions[:limit]
    
    def get_user_requests(self, user_id: str, limit: int = 10) -> List[AIRequest]:
        """获取用户AI请求记录"""
        user_requests = [
            r for r in self.ai_requests.values() 
            if r.user_id == user_id
        ]
        user_requests.sort(key=lambda x: x.created_at, reverse=True)
        return user_requests[:limit]
    
    def validate_api_key(self, api_key: str) -> Optional[str]:
        """验证API密钥，返回用户ID"""
        return self.api_keys.get(api_key)# ==================== 服务类 ====================

class PaymentService:
    """支付服务"""
    
    def __init__(self, db: Database):
        self.db = db
        self.service_prices = {
            "chat": 0.1,      # 聊天服务：0.1元/次
            "image": 0.5,     # 图像生成：0.5元/次
            "audio": 0.3,     # 音频服务：0.3元/次
            "video": 1.0,     # 视频服务：1.0元/次
            "document": 0.2,  # 文档处理：0.2元/次
            "code": 0.15      # 代码生成：0.15元/次
        }
    
    def authenticate(self, api_key: str) -> AuthResponse:
        """用户鉴权"""
        user_id = self.db.validate_api_key(api_key)
        
        if not user_id:
            return AuthResponse(
                authenticated=False,
                message="无效的API密钥"
            )
        
        user = self.db.get_user(user_id)
        if not user or not user.is_active:
            return AuthResponse(
                authenticated=False,
                message="用户不存在或已被禁用"
            )
        
        return AuthResponse(
            authenticated=True,
            user_id=user_id,
            balance=user.balance,
            message="鉴权成功"
        )
    
    def recharge(self, user_id: str, request: RechargeRequest) -> RechargeResponse:
        """用户充值"""
        user = self.db.get_user(user_id)
        if not user:
            return RechargeResponse(
                success=False,
                transaction_id="",
                new_balance=0.0,
                message="用户不存在"
            )
        
        # 生成交易ID
        transaction_id = f"tx_{int(time.time())}_{uuid.uuid4().hex[:8]}"
        
        # 创建交易记录
        transaction = Transaction(
            transaction_id=transaction_id,
            user_id=user_id,
            type="recharge",
            amount=request.amount,
            description=request.description or f"通过{request.payment_method}充值",
            status="completed",
            created_at=datetime.now().isoformat()
        )
        
        # 更新用户余额
        self.db.update_user_balance(user_id, request.amount)
        
        # 保存交易记录
        self.db.add_transaction(transaction)
        
        # 获取更新后的余额
        updated_user = self.db.get_user(user_id)
        
        return RechargeResponse(
            success=True,
            transaction_id=transaction_id,
            new_balance=updated_user.balance,
            message=f"充值成功：{request.amount}元"
        )

    def deduct(self, user_id: str, request: DeductionRequest) -> DeductionResponse:
        """扣费处理"""
        user = self.db.get_user(user_id)
        if not user:
            return DeductionResponse(
                success=False,
                request_id="",
                remaining_balance=0.0,
                message="用户不存在"
            )
        
        # 检查余额是否充足
        if user.balance < request.cost:
            return DeductionResponse(
                success=False,
                request_id="",
                remaining_balance=user.balance,
                message=f"余额不足，当前余额：{user.balance}元，需要：{request.cost}元，请充值"
            )
        
        # 生成请求ID
        request_id = f"req_{int(time.time())}_{uuid.uuid4().hex[:8]}"
        
        # 创建AI请求记录
        ai_request = AIRequest(
            request_id=request_id,
            user_id=user_id,
            service_type=request.service_type,
            cost=request.cost,
            status="completed",
            created_at=datetime.now().isoformat(),
            completed_at=datetime.now().isoformat(),
            metadata=request.metadata
        )
        
        # 生成交易ID
        transaction_id = f"tx_{int(time.time())}_{uuid.uuid4().hex[:8]}"
        
        # 创建扣费交易记录
        transaction = Transaction(
            transaction_id=transaction_id,
            user_id=user_id,
            type="deduction",
            amount=-request.cost,  # 负值表示扣费
            description=f"AI服务扣费：{request.service_type}",
            status="completed",
            created_at=datetime.now().isoformat(),
            reference_id=request_id
        )
        
        # 更新用户余额
        self.db.update_user_balance(user_id, -request.cost)
        
        # 保存记录
        self.db.add_ai_request(ai_request)
        self.db.add_transaction(transaction)
        
        # 获取更新后的余额
        updated_user = self.db.get_user(user_id)
        
        return DeductionResponse(
            success=True,
            request_id=request_id,
            remaining_balance=updated_user.balance,
            message=f"扣费成功：{request.cost}元，剩余余额：{updated_user.balance}元"
        )
    
    def get_service_price(self, service_type: str) -> float:
        """获取服务价格"""
        return self.service_prices.get(service_type, 0.1)  # 默认0.1元
    
    def get_user_info(self, user_id: str) -> Optional[User]:
        """获取用户信息"""
        return self.db.get_user(user_id)
    
    def get_user_transactions(self, user_id: str, limit: int = 10) -> List[Transaction]:
        """获取用户交易记录"""
        return self.db.get_user_transactions(user_id, limit)
    
    def get_user_requests(self, user_id: str, limit: int = 10) -> List[AIRequest]:
        """获取用户AI请求记录"""
        return self.db.get_user_requests(user_id, limit)# ==================== FastAPI应用 ====================

# 初始化数据库和服务
db = Database()
payment_service = PaymentService(db)
security = HTTPBearer()

app = FastAPI(
    title="AI请求支付服务",
    description="提供用户充值、扣费、鉴权接口，按次收费",
    version="1.0.0"
)

def get_current_user(credentials: HTTPAuthorizationCredentials = Depends(security)):
    """获取当前用户"""
    api_key = credentials.credentials
    auth_result = payment_service.authenticate(api_key)
    
    if not auth_result.authenticated:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail=auth_result.message
        )
    
    return auth_result.user_id

# ==================== API接口 ====================

@app.get("/")
async def root():
    """根路径"""
    return {
        "service": "AI请求支付服务",
        "version": "1.0.0",
        "endpoints": {
            "auth": "/auth",
            "recharge": "/recharge",
            "deduct": "/deduct",
            "user_info": "/user/info",
            "transactions": "/user/transactions",
            "requests": "/user/requests",
            "prices": "/prices"
        }
    }

@app.get("/auth", response_model=AuthResponse)
async def authenticate(api_key: str = Header(..., alias="X-API-Key")):
    """用户鉴权"""
    return payment_service.authenticate(api_key)

@app.post("/recharge", response_model=RechargeResponse)
async def recharge(
    request: RechargeRequest,
    user_id: str = Depends(get_current_user)
):
    """用户充值"""
    return payment_service.recharge(user_id, request)

@app.post("/deduct", response_model=DeductionResponse)
async def deduct(
    request: DeductionRequest,
    user_id: str = Depends(get_current_user)
):
    """扣费处理"""
    return payment_service.deduct(user_id, request)

@app.get("/user/info")
async def get_user_info(user_id: str = Depends(get_current_user)):
    """获取用户信息"""
    user = payment_service.get_user_info(user_id)
    if not user:
        raise HTTPException(status_code=404, detail="用户不存在")
    return user

@app.get("/user/transactions")
async def get_transactions(
    limit: int = 10,
    user_id: str = Depends(get_current_user)
):
    """获取用户交易记录"""
    transactions = payment_service.get_user_transactions(user_id, limit)
    return {
        "user_id": user_id,
        "count": len(transactions),
        "transactions": transactions
    }

@app.get("/user/requests")
async def get_requests(
    limit: int = 10,
    user_id: str = Depends(get_current_user)
):
    """获取用户AI请求记录"""
    requests = payment_service.get_user_requests(user_id, limit)
    return {
        "user_id": user_id,
        "count": len(requests),
        "requests": requests
    }

@app.get("/prices")
async def get_prices():
    """获取服务价格列表"""
    return {
        "service_prices": payment_service.service_prices,
        "currency": "CNY (元)"
    }

@app.get("/health")
async def health_check():
    """健康检查"""
    return {
        "status": "healthy",
        "timestamp": datetime.now().isoformat(),
        "users_count": len(db.users),
        "transactions_count": len(db.transactions),
        "requests_count": len(db.ai_requests)
    }# ==================== 测试代码 ====================

def run_tests():
    """运行测试用例"""
    print("=" * 60)
    print("AI支付服务测试开始")
    print("=" * 60)
    
    test_results = []
    
    # 测试1：鉴权测试
    print("\n1. 鉴权测试")
    print("-" * 40)
    
    # 有效API密钥测试
    auth_result = payment_service.authenticate("api_key_user_001")
    test_results.append({
        "test": "有效API密钥鉴权",
        "result": "PASS" if auth_result.authenticated else "FAIL",
        "message": auth_result.message,
        "balance": auth_result.balance
    })
    print(f"  有效API密钥: {auth_result.message}, 余额: {auth_result.balance}")
    
    # 无效API密钥测试
    auth_result = payment_service.authenticate("invalid_api_key")
    test_results.append({
        "test": "无效API密钥鉴权",
        "result": "PASS" if not auth_result.authenticated else "FAIL",
        "message": auth_result.message
    })
    print(f"  无效API密钥: {auth_result.message}")
    
    # 测试2：充值测试
    print("\n2. 充值测试")
    print("-" * 40)
    
    recharge_request = RechargeRequest(amount=50.0, payment_method="alipay")
    recharge_result = payment_service.recharge("user_001", recharge_request)
    test_results.append({
        "test": "用户充值",
        "result": "PASS" if recharge_result.success else "FAIL",
        "message": recharge_result.message,
        "new_balance": recharge_result.new_balance
    })
    print(f"  充值结果: {recharge_result.message}, 新余额: {recharge_result.new_balance}")
    
    # 测试3：扣费测试（余额充足）
    print("\n3. 扣费测试（余额充足）")
    print("-" * 40)
    
    deduction_request = DeductionRequest(service_type="chat", cost=0.1)
    deduction_result = payment_service.deduct("user_001", deduction_request)
    test_results.append({
        "test": "余额充足扣费",
        "result": "PASS" if deduction_result.success else "FAIL",
        "message": deduction_result.message,
        "remaining_balance": deduction_result.remaining_balance
    })
    print(f"  扣费结果: {deduction_result.message}, 剩余余额: {deduction_result.remaining_balance}")
    
    # 测试4：扣费测试（余额不足）
    print("\n4. 扣费测试（余额不足）")
    print("-" * 40)
    
    # 先获取低余额用户信息
    low_balance_user = payment_service.get_user_info("user_003")
    print(f"  低余额用户当前余额: {low_balance_user.balance}")
    
    deduction_request = DeductionRequest(service_type="image", cost=10.0)
    deduction_result = payment_service.deduct("user_003", deduction_request)
    test_results.append({
        "test": "余额不足扣费",
        "result": "PASS" if not deduction_result.success else "FAIL",
        "message": deduction_result.message,
        "remaining_balance": deduction_result.remaining_balance
    })
    print(f"  扣费结果: {deduction_result.message}")
    
    # 测试5：获取用户信息
    print("\n5. 用户信息查询测试")
    print("-" * 40)
    
    user_info = payment_service.get_user_info("user_002")
    test_results.append({
        "test": "用户信息查询",
        "result": "PASS" if user_info else "FAIL",
        "user_id": user_info.user_id if user_info else None,
        "balance": user_info.balance if user_info else None
    })
    print(f"  用户ID: {user_info.user_id}, 余额: {user_info.balance}")
    
    # 测试6：获取交易记录
    print("\n6. 交易记录查询测试")
    print("-" * 40)
    
    transactions = payment_service.get_user_transactions("user_001", 5)
    test_results.append({
        "test": "交易记录查询",
        "result": "PASS" if len(transactions) > 0 else "FAIL",
        "count": len(transactions)
    })
    print(f"  交易记录数量: {len(transactions)}")
    for i, tx in enumerate(transactions[:3], 1):
        print(f"  交易{i}: {tx.type} {tx.amount}元 - {tx.description}")
    
    # 测试7：获取服务价格
    print("\n7. 服务价格查询测试")
    print("-" * 40)
    
    chat_price = payment_service.get_service_price("chat")
    image_price = payment_service.get_service_price("image")
    test_results.append({
        "test": "服务价格查询",
        "result": "PASS",
        "chat_price": chat_price,
        "image_price": image_price
    })
    print(f"  聊天服务价格: {chat_price}元/次")
    print(f"  图像生成价格: {image_price}元/次")
    
    # 汇总测试结果
    print("\n" + "=" * 60)
    print("测试结果汇总")
    print("=" * 60)
    
    passed = sum(1 for r in test_results if r["result"] == "PASS")
    total = len(test_results)
    
    print(f"总测试数: {total}")
    print(f"通过数: {passed}")
    print(f"失败数: {total - passed}")
    print(f"通过率: {passed/total*100:.1f}%")
    
    print("\n详细结果:")
    for i, result in enumerate(test_results, 1):
        print(f"{i:2d}. {result['test']:20s} - {result['result']:4s} - {result.get('message', '')}")
    
    return test_results

# ==================== 主程序 ====================

if __name__ == "__main__":
    # 运行测试
    test_results = run_tests()
    
    # 启动FastAPI服务器
    print("\n" + "=" * 60)
    print("启动FastAPI服务器...")
    print("=" * 60)
    print("服务器运行在: http://127.0.0.1:8000")
    print("API文档: http://127.0.0.1:8000/docs")
    print("按 Ctrl+C 停止服务器")
    
    uvicorn.run(
        app,
        host="127.0.0.1",
        port=8000,
        log_level="info"
    )
